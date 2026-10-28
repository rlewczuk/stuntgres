package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/stored"
)

func TestTriggerModeStageCancellation(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		for _, clones := range []bool{false, true} {
			name := map[bool]string{false: "implicit", true: "explicit"}[explicit] + map[bool]string{false: "-fk", true: "-clones"}[clones]
			t.Run(name, func(t *testing.T) {
				e, s := triggerModeUnitSetup(t)
				for _, q := range []string{
					"CREATE TABLE mode_tree(id integer,v integer) PARTITION BY RANGE(id)",
					"CREATE TABLE mode_leaf PARTITION OF mode_tree FOR VALUES FROM(0) TO(100)",
					"CREATE TRIGGER a BEFORE INSERT ON mode_tree FOR EACH ROW EXECUTE FUNCTION mode_fn()",
				} {
					sessionSQL(t, s, q)
				}
				sessionSQL(t, s, "INSERT INTO mode_parent VALUES(1,5)")
				if explicit {
					sessionSQL(t, s, "BEGIN")
				}
				ast, description := sequencePreparedDescription(t, e, s, "SELECT COALESCE(-(7::integer),0) AS v FROM mode_parent")
				result, err := sequencePreparedExecute(e, s, ast, &description)
				if err != nil {
					t.Fatal(err)
				}
				result.Rows.Close()
				if description.Analysis == nil || description.Folds == nil || len(description.Folds.Current().Entries) == 0 {
					t.Fatal("missing accepted ownership")
				}
				analysis, folds := description.Analysis, description.Folds.Current()
				base := e.CatalogRead()
				journal := s.Transactions.Catalog
				generation := s.DiscardPlansGeneration
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
				q := "ALTER TABLE mode_parent DISABLE TRIGGER ALL"
				if clones {
					q = "ALTER TABLE mode_tree DISABLE TRIGGER a"
				}
				if _, err = scope.Execute(enginetest.Statement(t, q), 0, nil); err != nil {
					scope.Finish(err)
					t.Fatal(err)
				}
				if scope.Catalog == nil {
					scope.Finish(errors.New("missing candidate"))
					t.Fatal("missing real stage")
				}
				e.Mu.Lock()
				candidate := e.PrivateCatalogLocked(scope.Catalog)
				e.Mu.Unlock()
				targets := []string{"mode_parent", "mode_child"}
				if clones {
					targets = []string{"mode_tree", "mode_leaf"}
				}
				for _, name := range targets {
					ref := enginetest.RoleTableRef(t, e, name)
					next := candidate.MustRelation(ref.Address)
					if next == base.MustRelation(ref.Address) {
						scope.Finish(errors.New("unchanged candidate"))
						t.Fatal("missing staged owner", name)
					}
					if name == "mode_child" {
						if next.Constraints.ForeignKeys[0].TriggerMode(0) != catalog.TriggerDisabled {
							t.Fatal("missing staged FK mode")
						}
					} else if stored.TriggersOf(next)[0].Mode() != catalog.TriggerDisabled {
						t.Fatal("missing staged user mode", name)
					}
				}
				if e.CatalogRead().State() != base.State() || s.Transactions.Catalog != journal {
					scope.Finish(errors.New("early publication"))
					t.Fatal("premature publication")
				}
				cancel()
				if err := scope.Finish(nil); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				if e.CatalogRead().State() != base.State() || s.Transactions.Catalog != journal || s.DiscardPlansGeneration != generation {
					t.Fatal("cancellation published catalog/journal/generation")
				}
				if description.Analysis != analysis || description.Folds.Current().Generation != folds.Generation || &description.Folds.Current().Entries[0] != &folds.Entries[0] || folds.Entries[0].Value.Type() == nil {
					t.Fatal("canceled publication changed accepted owner/backing")
				}
			})
		}
	}
}

func TestTriggerModePreparedPublication(t *testing.T) {
	e, s := triggerModeUnitSetup(t)
	sessionSQL(t, s, "INSERT INTO mode_parent VALUES(1,5)")
	ast, d := sequencePreparedDescription(t, e, s, "SELECT COALESCE(-(7::integer),0) AS v FROM mode_parent")
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
	originalAnalysis, originalFolds := d.Analysis, d.Folds.Current()
	generation := s.DiscardPlansGeneration
	sessionSQL(t, s, "ALTER TABLE mode_parent ENABLE TRIGGER a")
	execute()
	if d.Analysis != originalAnalysis || s.DiscardPlansGeneration != generation || &d.Folds.Current().Entries[0] != &originalFolds.Entries[0] {
		t.Fatal("no-op replaced accepted owner/backing")
	}
	for _, queries := range [][]string{
		{"ALTER TABLE mode_parent DISABLE TRIGGER a"},
		{"ALTER TABLE mode_parent ENABLE REPLICA TRIGGER a"},
		{"ALTER TABLE mode_parent ENABLE ALWAYS TRIGGER a"},
		{"ALTER TABLE mode_parent ENABLE TRIGGER a"},
		{"SET session_replication_role=replica", "RESET session_replication_role"},
		{"BEGIN", "SAVEPOINT modes", "SET LOCAL session_replication_role=replica", "ROLLBACK TO modes", "COMMIT"},
	} {
		before, folds := d.Analysis, d.Folds.Current()
		discard := s.DiscardPlansGeneration
		for _, q := range queries {
			sessionSQL(t, s, q)
		}
		execute()
		accepted := d.Folds.Current()
		if d.Analysis == before || d.PlanOwner != s.SessionToken || d.PlanGeneration != s.DiscardPlansGeneration || d.Folds.Analysis != d.Analysis {
			t.Fatal("stale analysis authority", queries)
		}
		if discard != s.DiscardPlansGeneration && (accepted.Generation <= folds.Generation || &accepted.Entries[0] == &folds.Entries[0]) {
			t.Fatal("role transition retained fold backing")
		}
		if folds.Entries[0].Value.Type() == nil || originalFolds.Entries[0].Value.Type() == nil {
			t.Fatal("retained immutable folds damaged")
		}
	}
	before, folds := d.Analysis, d.Folds.Current()
	_, err := sequencePreparedExecute(e, s, enginetest.Statement(t, "ALTER TABLE mode_parent DISABLE TRIGGER a, ENABLE TRIGGER missing"), nil)
	var state interface{ SQLState() string }
	if !errors.As(err, &state) || state.SQLState() != "42704" {
		t.Fatal(err)
	}
	execute()
	if d.Analysis != before || &d.Folds.Current().Entries[0] != &folds.Entries[0] || d.Folds.Current().Generation != folds.Generation {
		t.Fatal("failed candidate changed accepted backing")
	}
	insert, held := sequencePreparedDescription(t, e, s, "INSERT INTO mode_parent VALUES(2,5)")
	for _, q := range []string{"ALTER TABLE mode_parent DISABLE TRIGGER a", "ALTER TABLE mode_parent ENABLE ALWAYS TRIGGER a", "SET session_replication_role=replica", "RESET session_replication_role"} {
		previous := held.Analysis
		sessionSQL(t, s, q)
		r, err := sequencePreparedExecute(e, s, insert, &held)
		if err != nil {
			t.Fatal(err)
		}
		if r.Rows != nil {
			r.Rows.Close()
		}
		if previous != nil && held.Analysis == previous {
			t.Fatal("held DML retained stale analysis", q)
		}
		// Keep the same held INSERT reusable without a uniqueness collision.
		sessionSQL(t, s, "DELETE FROM mode_parent WHERE id=2")
	}
}
