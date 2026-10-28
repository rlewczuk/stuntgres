package core_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func copyProgressFacts(t *testing.T, e *core.Engine, op *session.Operation) []pgcatalog.CopyProgressFact {
	t.Helper()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	var facts []pgcatalog.CopyProgressFact
	runtime := core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, Auth: auth})
	if err := runtime.VisitCopyProgress(op.Context(), func(f pgcatalog.CopyProgressFact) error { facts = append(facts, f); return nil }); err != nil {
		t.Fatal(err)
	}
	return facts
}

func TestCopyProgressActualInsertionBoundaries(t *testing.T) {
	for _, kind := range []string{"plain", "unique", "partition", "trigger"} {
		for _, n := range []int{999, 1000} {
			t.Run(fmt.Sprintf("%s/%d", kind, n), func(t *testing.T) {
				e := engine.New()
				worker := sessionStart(t, e, nil)
				ddl := "CREATE TABLE copy_progress_unit(v INT)"
				if kind == "unique" {
					ddl = "CREATE TABLE copy_progress_unit(v INT PRIMARY KEY)"
				}
				if kind == "partition" {
					ddl = "CREATE TABLE copy_progress_unit(v INT PRIMARY KEY) PARTITION BY RANGE(v)"
				}
				sessionSQL(t, worker, ddl)
				if kind == "partition" {
					sessionSQL(t, worker, "CREATE TABLE copy_progress_child PARTITION OF copy_progress_unit FOR VALUES FROM (0) TO (2000)")
				}
				if kind == "trigger" {
					sessionSQL(t, worker, "CREATE FUNCTION copy_progress_suppress() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN CASE WHEN NEW.v=2 THEN NULL ELSE NEW END; END$$")
					sessionSQL(t, worker, "CREATE TRIGGER suppress BEFORE INSERT ON copy_progress_unit FOR EACH ROW EXECUTE FUNCTION copy_progress_suppress()")
				}
				observer := sessionStart(t, e, nil)
				observerOp := transactionOperation(t, observer)
				defer observerOp.Done()
				op := transactionOperation(t, worker)
				scope, err := e.StartStatement(op)
				if err != nil {
					op.Done()
					t.Fatal(err)
				}
				var payload strings.Builder
				for i := 1; i <= n; i++ {
					fmt.Fprintf(&payload, "%d\n", i)
				}
				reader := &copyUnitReader{first: true, prefix: strings.NewReader(payload.String()), entered: make(chan struct{}), release: make(chan struct{})}
				type outcome struct {
					count int64
					err   error
				}
				done := make(chan outcome, 1)
				joined := false
				defer func() {
					if !joined {
						close(reader.release)
						<-done
					}
				}()
				syntax := enginetest.Statement(t, "COPY copy_progress_unit FROM STDIN").(*parser.Copy)
				go func() {
					count, err := scope.Copy(syntax, reader, nil)
					err = scope.Finish(err)
					op.Done()
					done <- outcome{count, err}
				}()
				select {
				case <-reader.entered:
				case out := <-done:
					joined = true
					t.Fatalf("COPY ended before actual read gate: %+v", out)
				}
				facts := copyProgressFacts(t, e, observerOp)
				if len(facts) != 1 {
					t.Fatalf("active facts=%+v", facts)
				}
				expected := int64(n)
				if kind == "trigger" {
					expected--
				}
				if (kind == "unique" || kind == "partition") && n < 1000 {
					expected = 0
				}
				before := facts[0]
				probe := core.ScopeCopyProgressProbe(scope)
				if before.TuplesProcessed != expected || before.BytesProcessed != int64(payload.Len()) || before.TuplesExcluded != 0 || before.TuplesSkipped != 0 || !before.Visible {
					t.Fatalf("actual completion boundary got=%+v expected=%d", before, expected)
				}
				close(reader.release)
				out := <-done
				joined = true
				final := int64(n)
				if kind == "trigger" {
					final--
				}
				if out.err != nil || out.count != final {
					t.Fatalf("COPY outcome=%+v expected=%d", out, final)
				}
				if f := probe(); f.TuplesProcessed != final {
					t.Fatalf("flush/application double-counted or lost rows: %+v expected=%d", f, final)
				}
				core.ScopeEndCopyProgress(scope) // Cleanup remains idempotent after Finish.
				if facts := copyProgressFacts(t, e, observerOp); len(facts) != 0 {
					t.Fatalf("retained completed command: %+v", facts)
				}
				if before.TuplesProcessed != expected || before.BytesProcessed != int64(payload.Len()) {
					t.Fatal("retained fact was borrowed")
				}
			})
		}
	}
}

func TestCopyProgressCancellationNoCompletion(t *testing.T) {
	e := engine.New()
	worker := sessionStart(t, e, nil)
	sessionSQL(t, worker, "CREATE TABLE t(v INT PRIMARY KEY)")
	op := transactionOperation(t, worker)
	scope, err := e.StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	observer := sessionStart(t, e, nil)
	observerOp := transactionOperation(t, observer)
	defer observerOp.Done()
	// A single unique row is pending, not a successful 1000-row flush.
	reader := &copyUnitReader{entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	joined := false
	defer func() {
		if !joined {
			close(reader.release)
			<-done
		}
	}()
	go func() {
		_, err := scope.Copy(copyUnitSyntax(), reader, nil)
		err = scope.Finish(err)
		op.Done()
		done <- err
	}()
	select {
	case <-reader.entered:
	case err := <-done:
		joined = true
		t.Fatal("COPY ended before cancellation gate", err)
	}
	probe := core.ScopeCopyProgressProbe(scope)
	if f := probe(); f.TuplesProcessed != 0 || f.BytesProcessed != 2 {
		t.Fatalf("pending row reported complete: %+v", f)
	}
	op.Cancel(context.Canceled)
	if f := probe(); f.TuplesProcessed != 0 {
		t.Fatalf("cancel forced completion before callback release: %+v", f)
	}
	close(reader.release)
	err = <-done
	joined = true
	if !errors.Is(err, context.Canceled) {
		t.Fatal("native cancellation identity", err)
	}
	if f := probe(); f.TuplesProcessed != 0 {
		t.Fatalf("cleanup forced terminal completion: %+v", f)
	}
	if f := copyProgressFacts(t, e, observerOp); len(f) != 0 {
		t.Fatal("canceled command retained", f)
	}
}

func TestCopyProgressExactEntryCleanup(t *testing.T) {
	e, _, op, scope := copyUnitStart(t, false)
	defer op.Done()
	defer scope.Finish(errors.New("registration unit complete"))
	observer := sessionStart(t, e, nil)
	observerOp := transactionOperation(t, observer)
	defer observerOp.Done()
	old := core.ScopeBeginCopyProgress(scope, 111, true)
	current := core.ScopeBeginCopyProgress(scope, 222, false)
	core.ScopeEndCopyProgressEntry(scope, old)
	core.ScopeEndCopyProgressEntry(scope, old)
	facts := copyProgressFacts(t, e, observerOp)
	if len(facts) != 1 || facts[0].Relation != 222 || facts[0].Command != "COPY TO" {
		t.Fatalf("stale cleanup removed newer registration: %+v", facts)
	}
	core.ScopeEndCopyProgressEntry(scope, current)
	core.ScopeEndCopyProgressEntry(scope, current)
	if facts := copyProgressFacts(t, e, observerOp); len(facts) != 0 {
		t.Fatal("idempotent cleanup retained entry", facts)
	}
}

func TestCopyProgressSharedViewer(t *testing.T) {
	e := engine.New()
	admin := sessionStart(t, e, nil)
	for _, q := range []string{
		"CREATE ROLE progress_worker",
		"CREATE ROLE progress_member",
		"CREATE ROLE progress_other",
		"CREATE ROLE progress_reader",
		"CREATE ROLE progress_monitor",
		"GRANT progress_worker TO progress_member",
		"GRANT pg_read_all_stats TO progress_reader",
		"GRANT pg_monitor TO progress_monitor",
	} {
		sessionSQL(t, admin, q)
	}
	roles := e.RoleStore.Published.Root.Read(e.RoleStore.Domain.Principal(10))
	owner := roles.LookupRef("progress_worker")
	for _, tc := range []struct {
		name    string
		visible bool
	}{
		{"postgres", true}, {"progress_worker", true}, {"progress_member", true},
		{"progress_other", false}, {"progress_reader", true}, {"progress_monitor", true},
	} {
		viewer, err := pgcatalog.NewProgressViewer(context.Background(), roles, roles.LookupRef(tc.name))
		if err != nil || viewer.Visible(owner) != tc.visible {
			t.Fatalf("shared COPY/vacuum viewer %s visible=%v err=%v", tc.name, viewer.Visible(owner), err)
		}
	}
}
