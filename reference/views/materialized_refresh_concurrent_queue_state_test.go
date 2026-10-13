package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func concurrentQueuedNoData(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			holder, first := fixture.LockConnections(t, p.Ctx, b.DB)
			second, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer second.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, second, b.Recorder, s) }
			concurrentSeed(run)
			concurrentIndex(run)
			prepared, err := second.PrepareContext(p.Ctx, concurrentRefreshSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer prepared.Close()
			holderPID, firstPID, secondPID := 0, 0, 0
			var oid int64
			if b.PG {
				holderPID = fixture.LockPID(t, p.Ctx, holder)
				firstPID = fixture.LockPID(t, p.Ctx, first)
				secondPID = fixture.LockPID(t, p.Ctx, second)
				oid = fixture.LockOID(t, p.Ctx, "concurrent_mv")
			}
			tx := fixture.QueryLockBegin(t, p.Ctx, holder)
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, concurrentOld())
			ctx, cancel := context.WithTimeout(p.Ctx, 15*time.Second)
			a, z := make(chan refreshQueuedOutcome, 1), make(chan refreshQueuedOutcome, 1)
			startedSecond, joinedFirst, joinedSecond := false, false, false
			defer func() {
				cancel()
				tx.Rollback()
				cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
				defer stop()
				for _, w := range []struct {
					started, joined bool
					done            <-chan refreshQueuedOutcome
				}{{true, joinedFirst, a}, {startedSecond, joinedSecond, z}} {
					if w.started && !w.joined {
						select {
						case <-w.done:
						case <-cleanup.Done():
							t.Error("state queue cleanup", cleanup.Err())
						}
					}
				}
			}()
			go refreshQueuedFirst(ctx, first, "REFRESH MATERIALIZED VIEW concurrent_mv WITH NO DATA", a)
			if b.PG {
				fixture.LockWaitBehind(t, ctx, firstPID, holderPID, oid, "AccessExclusiveLock")
			} else {
				concurrentNativeWait(t, ctx, "refreshQueuedFirst", "acquireMaterializedRefresh", a, &joinedFirst)
			}
			startedSecond = true
			go refreshQueuedSecond(ctx, prepared, z)
			if b.PG {
				fixture.LockWaitBehind(t, ctx, secondPID, firstPID, oid, "ExclusiveLock")
			} else {
				concurrentNativeWait(t, ctx, "refreshQueuedSecond", "acquireMaterializedRefresh", z, &joinedSecond)
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			refreshQueuedJoin(t, ctx, a, &joinedFirst)
			select {
			case outcome := <-z:
				joinedSecond = true
				if outcome.result != nil {
					t.Fatal("unpopulated result")
				}
				d := fixture.ControlsReferenceDiagnostic("0A000", "CONCURRENTLY cannot be used when the materialized view is not populated")
				fixture.AssertDiagnostic(t, outcome.err, &d)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			fixture.MaterializedViewPopulation(run, "concurrent_mv", false)
			run(fixture.MaterializedViewUnpopulated("concurrent_mv"))
			run(fixture.Exec("REFRESH MATERIALIZED VIEW concurrent_mv", 0))
			run(fixture.Exec(concurrentRefreshSQL, 0))
			run(concurrentOld())
		})
	}
}

func concurrentQueuedPlain(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			holder, first := fixture.LockConnections(t, p.Ctx, b.DB)
			second, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer second.Close()
			reader, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, reader, b.Recorder, s) }
			concurrentSeed(run)
			concurrentIndex(run)
			concurrentChange(run)
			stmt, err := second.PrepareContext(p.Ctx, "REFRESH MATERIALIZED VIEW concurrent_mv")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			holderPID, firstPID, secondPID := 0, 0, 0
			var base, target int64
			if b.PG {
				holderPID = fixture.LockPID(t, p.Ctx, holder)
				firstPID = fixture.LockPID(t, p.Ctx, first)
				secondPID = fixture.LockPID(t, p.Ctx, second)
				base = fixture.LockOID(t, p.Ctx, "concurrent_base")
				target = fixture.LockOID(t, p.Ctx, "concurrent_mv")
			}
			tx := fixture.QueryLockBegin(t, p.Ctx, holder)
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("LOCK TABLE concurrent_base IN ACCESS EXCLUSIVE MODE", 0))
			ctx, cancel := context.WithTimeout(p.Ctx, 15*time.Second)
			a, z := make(chan refreshQueuedOutcome, 1), make(chan refreshQueuedOutcome, 1)
			startedSecond, joinedFirst, joinedSecond := false, false, false
			defer func() {
				cancel()
				tx.Rollback()
				cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
				defer stop()
				for _, w := range []struct {
					started, joined bool
					done            <-chan refreshQueuedOutcome
				}{{true, joinedFirst, a}, {startedSecond, joinedSecond, z}} {
					if w.started && !w.joined {
						select {
						case <-w.done:
						case <-cleanup.Done():
							t.Error("plain queue cleanup", cleanup.Err())
						}
					}
				}
			}()
			go refreshQueuedFirst(ctx, first, concurrentRefreshSQL, a)
			if b.PG {
				fixture.LockWaitBehind(t, ctx, firstPID, holderPID, base, "AccessShareLock")
			} else {
				concurrentNativeWait(t, ctx, "refreshQueuedFirst", "acquireBoundViewRelationModes", a, &joinedFirst)
			}
			startedSecond = true
			go refreshQueuedSecond(ctx, stmt, z)
			if b.PG {
				fixture.LockWaitBehind(t, ctx, secondPID, firstPID, target, "AccessExclusiveLock")
			} else {
				concurrentNativeWait(t, ctx, "refreshQueuedSecond", "acquireMaterializedRefresh", z, &joinedSecond)
			}
			// Unlike queued EXCLUSIVE, queued AX is an admission barrier for SELECT.
			run(fixture.Exec("SET lock_timeout='100ms'", 0))
			diagnostic := fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
			diagnostic.Position = "17"
			s := fixture.ErrorDiagnostic("SELECT i,s FROM concurrent_mv", diagnostic)
			s.SelectRows = true
			run(s)
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			refreshQueuedJoin(t, ctx, a, &joinedFirst)
			refreshQueuedJoin(t, ctx, z, &joinedSecond)
			run(concurrentNew())
			run(fixture.Exec("RESET lock_timeout", 0))
		})
	}
}
