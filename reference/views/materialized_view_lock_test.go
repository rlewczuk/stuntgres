package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMaterializedViewLocks(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, peer, b.Recorder, s) }
			materializedViewSeed(run)
			run(fixture.Exec("CREATE MATERIALIZED VIEW mv_lock AS SELECT i,s FROM mv_base", 3))
			tx, err := a.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, materializedViewBag("mv_lock"))
			run(fixture.Exec("SET lock_timeout='100ms'", 0))
			run(fixture.ImplicitError("DROP MATERIALIZED VIEW mv_lock", fixture.Diagnostic{Code: "55P03", Message: "canceling statement due to lock timeout"}))
			run(materializedViewBag("mv_lock"))
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			// A blocked source relation must not affect reads of already stored rows.
			source, err := a.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer source.Rollback()
			fixture.RunStep(t, p.Ctx, source, b.Recorder, fixture.Exec("LOCK TABLE mv_base IN ACCESS EXCLUSIVE MODE", 0))
			run(materializedViewBag("mv_lock"))
			run(fixture.ImplicitError("CREATE MATERIALIZED VIEW mv_lock_failed AS SELECT i,s FROM mv_base", fixture.Diagnostic{Code: "55P03", Message: "canceling statement due to lock timeout", Position: "60"}))
			fixture.QueryTableCreateAbsent(t, run, b.PG, "mv_lock_failed")
			if err := source.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(fixture.Exec("RESET lock_timeout", 0))
			run(fixture.Exec("CREATE MATERIALIZED VIEW mv_lock_failed AS SELECT i,s FROM mv_base", 3))
			run(materializedViewBag("mv_lock_failed"))
			// lib/pq can complete fast Exec despite a precanceled Context.
			// Native guarantees no publication; real lock-timeout atomicity above is shared.
			if !b.PG {
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				result, err := peer.ExecContext(ctx, "CREATE MATERIALIZED VIEW mv_canceled AS SELECT i,s FROM mv_base")
				if result != nil || !errors.Is(err, context.Canceled) {
					t.Fatalf("precanceled result=%v error=%v", result, err)
				}
				fixture.QueryTableCreateAbsent(t, run, b.PG, "mv_canceled")
			}
			run(materializedViewBag("mv_lock"))
			run(fixture.Exec("DROP MATERIALIZED VIEW mv_lock,mv_lock_failed", 0))
			fixture.QueryTableCreateAbsent(t, run, b.PG, "mv_lock")
		})
	}
}
