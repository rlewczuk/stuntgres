package reference_test

import (
	"bytes"
	"context"
	"errors"
	"runtime"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMaterializedRefreshLocks(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			holder, peer := fixture.LockConnections(t, p.Ctx, b.DB)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, peer, b.Recorder, s) }
			refreshSeed(run)
			run(fixture.Exec("INSERT INTO mv_base VALUES(3,'c')", 1))
			run(fixture.Exec("SET lock_timeout='100ms'", 0))
			tx := fixture.QueryLockBegin(t, p.Ctx, holder)
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, materializedViewBag("refresh_mv"))
			// Protocol Describe must not wait for ACCESS EXCLUSIVE.
			held, err := peer.PrepareContext(p.Ctx, "REFRESH MATERIALIZED VIEW refresh_mv")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: peer, Statement: held, SQL: "REFRESH MATERIALIZED VIEW refresh_mv"}, b.Recorder, fixture.ErrorDiagnostic("REFRESH MATERIALIZED VIEW refresh_mv", fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")))
			run(materializedViewBag("refresh_mv"))
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			source := fixture.QueryLockBegin(t, p.Ctx, holder)
			fixture.RunStep(t, p.Ctx, source, b.Recorder, fixture.Exec("LOCK TABLE mv_base IN ACCESS EXCLUSIVE MODE", 0))
			run(fixture.ErrorDiagnostic("REFRESH MATERIALIZED VIEW refresh_mv", fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")))
			run(materializedViewBag("refresh_mv"))
			run(fixture.Exec("REFRESH MATERIALIZED VIEW refresh_mv WITH NO DATA", 0))
			fixture.MaterializedViewPopulation(run, "refresh_mv", false)
			run(fixture.MaterializedViewUnpopulated("refresh_mv"))
			if err := source.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(fixture.Exec("RESET lock_timeout", 0))
			run(fixture.Exec("REFRESH MATERIALIZED VIEW refresh_mv", 0))
			run(refreshBag("refresh_mv", [][]any{{int64(1), "a"}, {int64(1), "a"}, {int64(2), nil}, {int64(3), "c"}}))
		})
	}
}

func TestReferenceMaterializedRefreshCancellation(t *testing.T) {
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
			refreshSeed(run)
			run(fixture.Exec("INSERT INTO mv_base VALUES(3,'c')", 1))
			var before bytes.Buffer
			if !b.PG {
				if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
					t.Fatal(err)
				}
			}
			tx := fixture.QueryLockBegin(t, p.Ctx, holder)
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, materializedViewBag("refresh_mv"))
			ctx, cancel := context.WithCancel(p.Ctx)
			done := make(chan error, 1)
			pid, blocker := 0, 0
			var oid int64
			if b.PG {
				pid = fixture.LockPID(t, p.Ctx, worker)
				blocker = fixture.LockPID(t, p.Ctx, holder)
				oid = fixture.LockOID(t, p.Ctx, "refresh_mv")
			}
			go func() { _, err := worker.ExecContext(ctx, "REFRESH MATERIALIZED VIEW refresh_mv"); done <- err }()
			joined := false
			defer func() {
				cancel()
				tx.Rollback()
				if !joined {
					select {
					case <-done:
					case <-p.Ctx.Done():
						t.Error("refresh worker failed to join", p.Ctx.Err())
					}
				}
			}()
			if b.PG {
				fixture.LockWaitBehind(t, p.Ctx, pid, blocker, oid, "AccessExclusiveLock")
				var signaled bool
				if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&signaled); err != nil || !signaled {
					t.Fatal(signaled, err)
				}
			} else {
				// Like fixture.BinaryConversionActiveCancel, observe the actual worker
				// frame, not elapsed time or a started signal. The AS holder keeps this
				// target AX request blocked; native LOCK on an MV stays unsupported.
				profiles := make([]runtime.StackRecord, 128)
				observed := false
				for !observed {
					select {
					case err := <-done:
						joined = true
						t.Fatal("refresh finished before queue observation", err)
					case <-p.Ctx.Done():
						t.Fatal(p.Ctx.Err())
					default:
					}
					n, ok := runtime.GoroutineProfile(profiles)
					if !ok {
						profiles = make([]runtime.StackRecord, n)
						continue
					}
					for _, profile := range profiles[:n] {
						frames := runtime.CallersFrames(profile.Stack())
						wait, refresh, worker := false, false, false
						for {
							frame, more := frames.Next()
							wait = wait || strings.HasSuffix(frame.Function, ".(*relationRequest).wait")
							refresh = refresh || strings.HasSuffix(frame.Function, ".acquireMaterializedRefresh")
							worker = worker || strings.Contains(frame.Function, "TestReferenceMaterializedRefreshCancellation")
							if !more {
								break
							}
						}
						if wait && refresh && worker {
							observed = true
							break
						}
					}
					if !observed {
						runtime.Gosched()
					}
				}
				cancel()
			}
			select {
			case err := <-done:
				joined = true
				if b.PG {
					want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
					fixture.AssertDiagnostic(t, err, &want)
				} else if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancel error=%v", err)
				}
			case <-p.Ctx.Done():
				t.Fatal("refresh cancellation join", p.Ctx.Err())
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(materializedViewBag("refresh_mv"))
			fixture.MaterializedViewPopulation(run, "refresh_mv", true)
			if !b.PG {
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(before.Bytes(), after.Bytes()) {
					t.Fatal("canceled refresh changed Dump")
				}
				canceled, stop := context.WithCancel(p.Ctx)
				stop()
				result, err := worker.ExecContext(canceled, "REFRESH MATERIALIZED VIEW refresh_mv WITH NO DATA")
				if result != nil || !errors.Is(err, context.Canceled) {
					t.Fatalf("precanceled result=%v err=%v", result, err)
				}
				run(materializedViewBag("refresh_mv"))
			}
			run(fixture.Exec("REFRESH MATERIALIZED VIEW refresh_mv", 0))
			run(refreshBag("refresh_mv", [][]any{{int64(1), "a"}, {int64(1), "a"}, {int64(2), nil}, {int64(3), "c"}}))
		})
	}
}
