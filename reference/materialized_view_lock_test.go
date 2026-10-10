package reference_test

import (
	"context"
	"errors"
	"testing"
)

func TestReferenceMaterializedViewLocks(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			a, peer := referenceLockConnections(t, p.ctx, b.db)
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, peer, b.recorder, s) }
			materializedViewSeed(run)
			run(referenceExec("CREATE MATERIALIZED VIEW mv_lock AS SELECT i,s FROM mv_base", 3))
			tx, err := a.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			referenceRunStep(t, p.ctx, tx, b.recorder, materializedViewBag("mv_lock"))
			run(referenceExec("SET lock_timeout='100ms'", 0))
			run(referenceImplicitError("DROP MATERIALIZED VIEW mv_lock", referenceDiagnostic{Code: "55P03", Message: "canceling statement due to lock timeout"}))
			run(materializedViewBag("mv_lock"))
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			// A blocked source relation must not affect reads of already stored rows.
			source, err := a.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer source.Rollback()
			referenceRunStep(t, p.ctx, source, b.recorder, referenceExec("LOCK TABLE mv_base IN ACCESS EXCLUSIVE MODE", 0))
			run(materializedViewBag("mv_lock"))
			run(referenceImplicitError("CREATE MATERIALIZED VIEW mv_lock_failed AS SELECT i,s FROM mv_base", referenceDiagnostic{Code: "55P03", Message: "canceling statement due to lock timeout", Position: "60"}))
			queryTableCreateAbsent(t, run, b.pg, "mv_lock_failed")
			if err := source.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(referenceExec("RESET lock_timeout", 0))
			run(referenceExec("CREATE MATERIALIZED VIEW mv_lock_failed AS SELECT i,s FROM mv_base", 3))
			run(materializedViewBag("mv_lock_failed"))
			// lib/pq can complete fast Exec despite a precanceled Context.
			// Native guarantees no publication; real lock-timeout atomicity above is shared.
			if !b.pg {
				ctx, cancel := context.WithCancel(p.ctx)
				cancel()
				result, err := peer.ExecContext(ctx, "CREATE MATERIALIZED VIEW mv_canceled AS SELECT i,s FROM mv_base")
				if result != nil || !errors.Is(err, context.Canceled) {
					t.Fatalf("precanceled result=%v error=%v", result, err)
				}
				queryTableCreateAbsent(t, run, b.pg, "mv_canceled")
			}
			run(materializedViewBag("mv_lock"))
			run(referenceExec("DROP MATERIALIZED VIEW mv_lock,mv_lock_failed", 0))
			queryTableCreateAbsent(t, run, b.pg, "mv_lock")
		})
	}
}
