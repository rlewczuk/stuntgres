package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceMaterializedRefreshConcurrentSourceMaterialized(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			holder, worker := fixture.LockConnections(t, p.Ctx, b.DB)
			observer, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer observer.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, observer, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE source_base(i INT,s TEXT)", 0))
			run(fixture.Exec("INSERT INTO source_base VALUES(1,'old'),(2,NULL)", 2))
			run(fixture.Exec("CREATE MATERIALIZED VIEW source_inner AS SELECT i,s FROM source_base", 2))
			run(fixture.Exec("CREATE UNIQUE INDEX source_inner_u ON source_inner(i)", 0))
			// Source MV precedes the gate's OID, matching the real native acquisition
			// order; it is a physical leaf, not a recursively refreshed producer.
			run(fixture.Exec("CREATE TABLE source_gate(i INT)", 0))
			run(fixture.Exec("INSERT INTO source_gate VALUES(9)", 1))
			run(fixture.Exec("CREATE MATERIALIZED VIEW source_outer AS SELECT m.i,m.s FROM source_inner m CROSS JOIN source_gate g", 2))
			run(fixture.Exec("CREATE UNIQUE INDEX source_outer_u ON source_outer(i)", 0))
			old := [][]any{{int64(1), "old"}, {int64(2), nil}}
			fresh := [][]any{{int64(1), "new"}, {int64(3), "three"}}
			tx := fixture.QueryLockBegin(t, p.Ctx, holder)
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("LOCK TABLE source_gate IN ACCESS EXCLUSIVE MODE", 0))
			ctx, cancel := context.WithTimeout(p.Ctx, 15*time.Second)
			done := make(chan refreshQueuedOutcome, 1)
			joined := false
			defer func() {
				cancel()
				tx.Rollback()
				if !joined {
					cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
					defer stop()
					select {
					case <-done:
					case <-cleanup.Done():
						t.Error("producer-source worker cleanup", cleanup.Err())
					}
				}
			}()
			pid, blocker := 0, 0
			var gateOID, innerOID int64
			if b.PG {
				pid = fixture.LockPID(t, p.Ctx, worker)
				blocker = fixture.LockPID(t, p.Ctx, holder)
				gateOID = fixture.LockOID(t, p.Ctx, "source_gate")
				innerOID = fixture.LockOID(t, p.Ctx, "source_inner")
			}
			go refreshQueuedFirst(ctx, worker, "REFRESH MATERIALIZED VIEW CONCURRENTLY source_outer", done)
			if b.PG {
				fixture.LockWaitBehind(t, ctx, pid, blocker, gateOID, "AccessShareLock")
				var granted bool
				if err := fixture.Admin.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND relation=$2 AND mode='AccessShareLock' AND granted)", pid, innerOID).Scan(&granted); err != nil || !granted {
					t.Fatal("producer did not retain source MV AS", granted, err)
				}
			} else {
				concurrentNativeWait(t, ctx, "refreshQueuedFirst", "acquireBoundViewRelationModes", done, &joined)
			}
			run(refreshBag("source_outer", old))
			run(fixture.Exec("UPDATE source_base SET s='new' WHERE i=1", 1))
			run(fixture.Exec("DELETE FROM source_base WHERE i=2", 1))
			run(fixture.Exec("INSERT INTO source_base VALUES(3,'three')", 1))
			publish, stop := context.WithTimeout(ctx, 3*time.Second)
			defer stop()
			fixture.RunStep(t, publish, observer, b.Recorder, fixture.Exec("REFRESH MATERIALIZED VIEW CONCURRENTLY source_inner", 0))
			run(refreshBag("source_inner", fresh))
			run(refreshBag("source_outer", old))
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			refreshQueuedJoin(t, ctx, done, &joined)
			// Independent release-source expectation: refresh_matview_datafill copies
			// the utility's active statement snapshot after its rewrite-lock wait.
			// Root oracle must settle this exact old/new timing before any native fix.
			run(refreshBag("source_outer", old))
			run(fixture.Prepared(refreshBag("source_outer", old)))
			run(fixture.Exec("REFRESH MATERIALIZED VIEW CONCURRENTLY source_outer", 0))
			run(refreshBag("source_outer", fresh))
			fixture.MaterializedViewPopulation(run, "source_outer", true)
		})
	}
}
