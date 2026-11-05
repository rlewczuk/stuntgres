package core_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
)

func analyzeProgressFacts(t *testing.T, e *core.Engine, op *session.Operation) []pgcatalog.AnalyzeProgressFact {
	t.Helper()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	var facts []pgcatalog.AnalyzeProgressFact
	r := core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, Auth: auth})
	if err := r.VisitAnalyzeProgress(op.Context(), func(f pgcatalog.AnalyzeProgressFact) error { facts = append(facts, f); return nil }); err != nil {
		t.Fatal(err)
	}
	return facts
}

func analyzeProgressScope(t *testing.T, e *core.Engine, worker *session.Session) (*core.StatementScope, *session.Operation) {
	t.Helper()
	op := enginetest.TransactionOperation(t, worker)
	scope, err := e.StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !scope.Finished {
			_ = scope.Finish(context.Canceled)
		}
		op.Done()
	})
	return scope, op
}

func analyzeProgressUnit(t *testing.T) (*core.Engine, *core.StatementScope, *session.Operation, ids.ObjectAddress) {
	t.Helper()
	e := engine.New()
	worker := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, worker, "CREATE TABLE analyze_progress_unit(k INT)")
	address := enginetest.CatalogRef(t, e, e.CatalogRead(), "analyze_progress_unit").Address
	scope, _ := analyzeProgressScope(t, e, worker)
	observer := enginetest.TransactionOperation(t, enginetest.StartSession(t, e, nil))
	t.Cleanup(observer.Done)
	return e, scope, observer, address
}

func analyzeProgressBegin(e *core.Engine, scope *core.StatementScope, relation ids.ObjectAddress) *core.AnalyzeProgress {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	return scope.BeginAnalyzeProgressLocked(relation)
}

func analyzeProgressOne(t *testing.T, e *core.Engine, observer *session.Operation) pgcatalog.AnalyzeProgressFact {
	t.Helper()
	facts := analyzeProgressFacts(t, e, observer)
	if len(facts) != 1 {
		t.Fatal(facts)
	}
	return facts[0]
}

func TestAnalyzeProgressLifecycle(t *testing.T) {
	for _, terminal := range []string{"success", "error", "cancel", "panic"} {
		t.Run(terminal, func(t *testing.T) {
			e, scope, observer, relation := analyzeProgressUnit(t)
			old := analyzeProgressBegin(e, scope, relation)
			old.SampleStart(129)
			old.SampleDone(128, 129)
			current := analyzeProgressBegin(e, scope, relation)
			old.End()
			old.End()
			old.Phase("finalizing analyze")
			f := analyzeProgressOne(t, e, observer)
			if old == current || f.Phase != "initializing" || f.SampleBlocksTotal != 0 || f.SampleBlocksScanned != 0 {
				t.Fatal("stale target touched replacement", f)
			}
			current.Phase("acquiring sample rows")
			current.SampleStart(129)
			current.SampleDone(128, 129)
			before := analyzeProgressOne(t, e, observer)
			var err error
			switch terminal {
			case "success":
				err = scope.Finish(nil)
			case "error":
				failure := errors.New("admitted work failed")
				err = scope.Finish(failure)
				if !errors.Is(err, failure) {
					t.Fatal(err)
				}
			case "cancel":
				scope.Op.Cancel(context.Canceled)
				err = scope.Finish(context.Canceled)
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case "panic":
				marker := &struct{}{}
				var recovered any
				func() {
					defer func() { recovered = recover() }()
					defer scope.Finish(context.Canceled)
					panic(marker)
				}()
				if recovered != marker {
					t.Fatal(recovered)
				}
			}
			if terminal == "success" && err != nil {
				t.Fatal(err)
			}
			current.End()
			current.End()
			current.SampleDone(129, 129)
			if facts := analyzeProgressFacts(t, e, observer); len(facts) != 0 {
				t.Fatal("terminal progress retained", facts)
			}
			if before.SampleBlocksScanned != 1 || before.Phase != "acquiring sample rows" {
				t.Fatal("saved facts mutated on cleanup", before)
			}
		})
	}
	t.Run("independent_workers", func(t *testing.T) {
		e, first, observer, relation := analyzeProgressUnit(t)
		second, _ := analyzeProgressScope(t, e, enginetest.StartSession(t, e, nil))
		p := analyzeProgressBegin(e, first, relation)
		q := analyzeProgressBegin(e, second, relation)
		if facts := analyzeProgressFacts(t, e, observer); len(facts) != 2 {
			t.Fatal(facts)
		}
		p.End()
		f := analyzeProgressOne(t, e, observer)
		if f.PID != int64(second.Op.Session.Token().ID()) {
			t.Fatal(f)
		}
		q.End()
	})
}

func TestAnalyzeProgressSnapshots(t *testing.T) {
	t.Run("cross_database", analyzeProgressCrossDatabase)
	e, scope, observer, relation := analyzeProgressUnit(t)
	p := analyzeProgressBegin(e, scope, relation)
	p.SampleStart(129)
	p.SampleDone(129, 129)
	p.ExtendedStart(3)
	p.ExtendedDone(2)
	p.Phase("acquiring inherited sample rows")
	p.ChildrenStart(3)
	f := analyzeProgressOne(t, e, observer)
	if f.SampleBlocksTotal != 2 || f.SampleBlocksScanned != 2 || f.ExtendedStatsComputed != 2 || f.CurrentChild != 0 || f.ChildTablesDone != 0 {
		t.Fatal("inherited entry reset retained facts", f)
	}
	p.ChildStart(relation.ObjectID)
	f = analyzeProgressOne(t, e, observer)
	if f.CurrentChild != relation.ObjectID || f.SampleBlocksTotal != 0 || f.SampleBlocksScanned != 0 {
		t.Fatal("child start is not atomic reset", f)
	}
	p.ChildDone(1)
	for _, total := range []int{1, 3} {
		p.ExtendedDone(2)
		p.ExtendedStart(total)
		retained := analyzeProgressOne(t, e, observer)
		if retained.Phase != "computing extended statistics" || retained.ExtendedStatsTotal != int64(total) || retained.ExtendedStatsComputed != 2 {
			t.Fatal("extended entry clamped/reset old computed", retained)
		}
		// A skipped inventory does not produce a completion update.
		if skipped := analyzeProgressOne(t, e, observer); skipped != retained {
			t.Fatal(skipped, retained)
		}
		p.ExtendedDone(1)
		if next := analyzeProgressOne(t, e, observer); next.ExtendedStatsComputed != 1 || retained.ExtendedStatsComputed != 2 {
			t.Fatal("first pass success or immutable snapshot", next, retained)
		}
	}
	// Update callbacks stay entry-only even while publication owns Engine.Mu.
	e.Mu.Lock()
	p.Phase("finalizing analyze")
	p.ChildrenStart(3)
	p.ChildStart(relation.ObjectID)
	p.ChildDone(1)
	e.Mu.Unlock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 4096 {
			p.ChildStart(relation.ObjectID)
			p.SampleStart(129)
			p.SampleDone(128, 129)
			p.SampleDone(129, 129)
		}
	}()
	defer func() { <-done }()
	for range 512 {
		f := analyzeProgressOne(t, e, observer)
		if f.SampleBlocksScanned < 0 || f.SampleBlocksScanned > f.SampleBlocksTotal || (f.SampleBlocksTotal != 0 && f.SampleBlocksTotal != 2) || f.CurrentChild != relation.ObjectID {
			t.Fatal("torn capture", f)
		}
	}
}

func TestAnalyzeProgressLogicalBlocks(t *testing.T) {
	e, scope, observer, relation := analyzeProgressUnit(t)
	p := analyzeProgressBegin(e, scope, relation)
	for _, tc := range []struct {
		n, k        int
		total, done int64
	}{
		{0, 0, 0, 0}, {1, 0, 1, 0}, {1, 1, 1, 1}, {127, 127, 1, 1}, {128, 127, 1, 0}, {128, 128, 1, 1}, {129, 127, 2, 0}, {129, 128, 2, 1}, {129, 129, 2, 2},
	} {
		t.Run(fmt.Sprintf("%dof%d", tc.k, tc.n), func(t *testing.T) {
			p.SampleStart(tc.n)
			p.SampleDone(tc.k, tc.n)
			f := analyzeProgressOne(t, e, observer)
			if f.SampleBlocksTotal != tc.total || f.SampleBlocksScanned != tc.done || f.DelayTime != 0 {
				t.Fatal(f)
			}
		})
	}
	max := int(^uint(0) >> 1)
	p.SampleStart(max)
	p.SampleDone(max-127, max)
	f := analyzeProgressOne(t, e, observer)
	want := int64(max/128) + 1
	if f.SampleBlocksTotal != want || f.SampleBlocksScanned != int64((max-127)/128) {
		t.Fatal("overflow near MaxInt", f, want)
	}
	p.SampleDone(max, max)
	if f := analyzeProgressOne(t, e, observer); f.SampleBlocksScanned != want {
		t.Fatal(f)
	}
}
