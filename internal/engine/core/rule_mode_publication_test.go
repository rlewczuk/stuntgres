package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/stored"
)

func TestRuleModeStageCancellation(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "implicit", true: "explicit"}[explicit], func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			for _, q := range []string{"CREATE TABLE src(id INT,v INT)", "CREATE TABLE sink(id INT,v INT)", "CREATE RULE r AS ON INSERT TO src DO ALSO INSERT INTO sink VALUES(NEW.id,NEW.v)"} {
				sessionSQL(t, s, q)
			}
			ref := enginetest.RoleTableRef(t, e, "src")
			original := e.CatalogRead().MustRelation(ref.Address)
			if explicit {
				sessionSQL(t, s, "BEGIN")
			}
			journal := s.Transactions.Catalog
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, err := s.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			_, err = scope.Execute(enginetest.Statement(t, "ALTER TABLE src DISABLE RULE r"), 0, nil)
			if err != nil {
				scope.Finish(err)
				t.Fatal(err)
			}
			if scope.Catalog == nil {
				scope.Finish(errors.New("missing candidate"))
				t.Fatal("ALTER did not reach real statement-local candidate")
			}
			e.Mu.Lock()
			candidate := e.PrivateCatalogLocked(scope.Catalog).MustRelation(ref.Address)
			e.Mu.Unlock()
			if candidate == original || stored.RulesOf(candidate)[0].Mode() != catalog.RuleDisabled {
				scope.Finish(errors.New("missing changed candidate"))
				t.Fatal("real candidate does not carry disabled mode")
			}
			// The scope owns a changed journal while the committed root is untouched.
			if e.CatalogRead().MustRelation(ref.Address) != original || s.Transactions.Catalog != journal {
				scope.Finish(errors.New("premature publication"))
				t.Fatal("candidate published before Finish")
			}
			cancel()
			if err := scope.Finish(nil); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if e.CatalogRead().MustRelation(ref.Address) != original || stored.RulesOf(original)[0].Mode() != catalog.RuleOrigin || s.Transactions.Catalog != journal {
				t.Fatal("canceled Finish accepted candidate or private journal")
			}
		})
	}
}

func TestRuleModePreparedPublication(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TABLE src(id INT,v INT)", "CREATE TABLE sink(id INT,v INT)", "INSERT INTO src VALUES(1,10)", "CREATE RULE r AS ON INSERT TO src DO ALSO INSERT INTO sink VALUES(NEW.id,NEW.v)"} {
		sessionSQL(t, s, q)
	}
	ast, d := sequencePreparedDescription(t, e, s, "SELECT COALESCE(-(7::integer),0) AS v FROM src")
	execute := func() {
		r, err := sequencePreparedExecute(e, s, ast, &d)
		if err != nil {
			t.Fatal(err)
		}
		r.Rows.Close()
	}
	execute()
	if d.Analysis == nil || d.Folds == nil || len(d.Folds.Current().Entries) == 0 {
		t.Fatal("missing accepted owners")
	}
	originalAnalysis := d.Analysis
	originalFolds := d.Folds.Current()
	generation := s.DiscardPlansGeneration
	sessionSQL(t, s, "ALTER TABLE src ENABLE RULE r")
	execute()
	if d.Analysis != originalAnalysis || s.DiscardPlansGeneration != generation || &d.Folds.Current().Entries[0] != &originalFolds.Entries[0] {
		t.Fatal("no-op mode discarded genuine accepted backing")
	}
	for _, transition := range [][]string{
		{"ALTER TABLE src DISABLE RULE r"},
		{"ALTER TABLE src ENABLE REPLICA RULE r"},
		{"ALTER TABLE src ENABLE ALWAYS RULE r"},
		{"ALTER TABLE src ENABLE RULE r"},
		{"SET session_replication_role=replica", "SET session_replication_role=origin"},
		{"SET session_replication_role=replica", "RESET session_replication_role"},
		{"BEGIN", "SET LOCAL session_replication_role=replica", "COMMIT"},
		{"BEGIN", "SAVEPOINT settings", "SET LOCAL session_replication_role=replica", "ROLLBACK TO settings", "COMMIT"},
		{"CREATE FUNCTION saved_mode() RETURNS INT LANGUAGE SQL SET session_replication_role=replica AS $$SELECT 1$$", "SELECT saved_mode()"},
	} {
		beforeAnalysis := d.Analysis
		beforeFolds := d.Folds.Current()
		beforeGeneration := s.DiscardPlansGeneration
		for _, q := range transition {
			sessionSQL(t, s, q)
		}
		execute()
		accepted := d.Folds.Current()
		if d.Analysis == beforeAnalysis || d.PlanOwner != s.SessionToken || d.PlanGeneration != s.DiscardPlansGeneration || d.Folds.Analysis != d.Analysis || d.Folds.Environment.Discard != s.DiscardPlansGeneration || len(accepted.Entries) == 0 {
			t.Fatal("transition retained stale accepted owners", transition)
		}
		if s.DiscardPlansGeneration != beforeGeneration && (accepted.Generation <= beforeFolds.Generation || &accepted.Entries[0] == &beforeFolds.Entries[0]) {
			t.Fatal("effective role transition retained stale fold generation/backing", transition)
		}
		if beforeFolds.Entries[0].Value.Type() == nil || originalFolds.Entries[0].Value.Type() == nil {
			t.Fatal("replacement mutated retained immutable fold")
		}
	}
	beforeAnalysis, beforeFolds := d.Analysis, d.Folds.Current()
	_, failure := sequencePreparedExecute(e, s, enginetest.Statement(t, "ALTER TABLE src DISABLE RULE r, ENABLE RULE missing"), nil)
	var state interface{ SQLState() string }
	if !errors.As(failure, &state) || state.SQLState() != "42704" {
		t.Fatal("expected genuine failed ALTER", failure)
	}
	execute()
	if d.Analysis != beforeAnalysis || d.Folds.Current().Generation != beforeFolds.Generation || &d.Folds.Current().Entries[0] != &beforeFolds.Entries[0] {
		t.Fatal("failed ALTER replaced accepted owners/backing")
	}
	// Genuine held DML analysis also follows catalog mode and effective setting.
	insert, held := sequencePreparedDescription(t, e, s, "INSERT INTO src VALUES(2,20)")
	for _, q := range []string{"ALTER TABLE src DISABLE RULE r", "ALTER TABLE src ENABLE ALWAYS RULE r", "SET session_replication_role=replica", "RESET session_replication_role"} {
		r, err := sequencePreparedExecute(e, s, insert, &held)
		if err != nil {
			t.Fatal(err)
		}
		if r.Rows != nil {
			r.Rows.Close()
		}
		previous := held.Analysis
		sessionSQL(t, s, q)
		r, err = sequencePreparedExecute(e, s, insert, &held)
		if err != nil {
			t.Fatal(err)
		}
		if r.Rows != nil {
			r.Rows.Close()
		}
		if held.Analysis == previous {
			t.Fatal("held mutation reused stale accepted analysis", q)
		}
	}
}
