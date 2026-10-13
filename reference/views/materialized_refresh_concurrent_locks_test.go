package reference_test

import (
	"bytes"
	"context"
	"errors"
	"runtime"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// Observe an actual worker plus a real lock-manager wait, not merely startup.
func concurrentNativeWait(t *testing.T, ctx context.Context, marker, acquisition string, done <-chan refreshQueuedOutcome, joined *bool) {
	t.Helper()
	profiles := make([]runtime.StackRecord, 128)
	for {
		select {
		case o := <-done:
			*joined = true
			t.Fatal("worker completed before lock observation", o.err)
		case <-ctx.Done():
			t.Fatal("lock observation deadline", ctx.Err())
		default:
		}
		n, ok := runtime.GoroutineProfile(profiles)
		if !ok {
			profiles = make([]runtime.StackRecord, n)
			continue
		}
		for _, profile := range profiles[:n] {
			frames := runtime.CallersFrames(profile.Stack())
			wait, owner, worker := false, false, false
			for {
				frame, more := frames.Next()
				wait = wait || strings.HasSuffix(frame.Function, ".(*relationRequest).wait")
				owner = owner || strings.HasSuffix(frame.Function, "."+acquisition)
				worker = worker || strings.HasSuffix(frame.Function, "."+marker)
				if !more {
					break
				}
			}
			if wait && owner && worker {
				return
			}
		}
		runtime.Gosched()
	}
}

func TestReferenceMaterializedRefreshConcurrentReaders(t *testing.T) {
	t.Run("source_wait", func(t *testing.T) { concurrentQueue(t, false, false) })
	t.Run("atomic_scans", concurrentAtomicScans)
}
func TestReferenceMaterializedRefreshConcurrentQueue(t *testing.T) {
	t.Run("concurrent_concurrent", func(t *testing.T) { concurrentQueue(t, true, false) })
	t.Run("plain_data_concurrent", func(t *testing.T) { concurrentQueue(t, true, true) })
	t.Run("plain_no_data_concurrent", concurrentQueuedNoData)
	t.Run("concurrent_plain", concurrentQueuedPlain)
}

func concurrentQueue(t *testing.T, second, plain bool) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			holder, first := fixture.LockConnections(t, p.Ctx, b.DB)
			reader, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, reader, b.Recorder, s) }
			concurrentSeed(run)
			concurrentIndex(run)
			concurrentChange(run)
			peer, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			prepared, err := peer.PrepareContext(p.Ctx, concurrentRefreshSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer prepared.Close()
			selectStmt, err := reader.PrepareContext(p.Ctx, "SELECT i,s FROM concurrent_mv")
			if err != nil {
				t.Fatal(err)
			}
			defer selectStmt.Close()
			firstPID, secondPID, holderPID := 0, 0, 0
			var baseOID, targetOID int64
			if b.PG {
				firstPID = fixture.LockPID(t, p.Ctx, first)
				secondPID = fixture.LockPID(t, p.Ctx, peer)
				holderPID = fixture.LockPID(t, p.Ctx, holder)
				baseOID = fixture.LockOID(t, p.Ctx, "concurrent_base")
				targetOID = fixture.LockOID(t, p.Ctx, "concurrent_mv")
			}
			tx := fixture.QueryLockBegin(t, p.Ctx, holder)
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("LOCK TABLE concurrent_base IN ACCESS EXCLUSIVE MODE", 0))
			ctx, cancel := context.WithTimeout(p.Ctx, 15*time.Second)
			firstDone, secondDone := make(chan refreshQueuedOutcome, 1), make(chan refreshQueuedOutcome, 1)
			startedSecond, joinedFirst, joinedSecond := false, false, false
			defer func() {
				cancel()
				tx.Rollback()
				cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
				defer stop()
				for _, w := range []struct {
					started, joined bool
					done            <-chan refreshQueuedOutcome
				}{{true, joinedFirst, firstDone}, {startedSecond, joinedSecond, secondDone}} {
					if w.started && !w.joined {
						select {
						case <-w.done:
						case <-cleanup.Done():
							t.Error("worker cleanup deadline", cleanup.Err())
						}
					}
				}
			}()
			q := concurrentRefreshSQL
			if plain {
				q = "REFRESH MATERIALIZED VIEW concurrent_mv"
			}
			go refreshQueuedFirst(ctx, first, q, firstDone)
			if b.PG {
				fixture.LockWaitBehind(t, ctx, firstPID, holderPID, baseOID, "AccessShareLock")
			} else {
				concurrentNativeWait(t, ctx, "refreshQueuedFirst", "acquireBoundViewRelationModes", firstDone, &joinedFirst)
			}
			if !plain {
				run(fixture.Exec("SET lock_timeout='100ms'", 0))
				run(concurrentOld())
				run(fixture.Prepared(concurrentOld()))
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: reader, Statement: selectStmt, SQL: "SELECT i,s FROM concurrent_mv"}, b.Recorder, concurrentOld())
			}
			if second {
				startedSecond = true
				go refreshQueuedSecond(ctx, prepared, secondDone)
				if b.PG {
					fixture.LockWaitBehind(t, ctx, secondPID, firstPID, targetOID, "ExclusiveLock")
				} else {
					concurrentNativeWait(t, ctx, "refreshQueuedSecond", "acquireMaterializedRefresh", secondDone, &joinedSecond)
				}
				if !plain {
					run(concurrentOld())
				} // SELECT also passes the queued EXCLUSIVE.
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			refreshQueuedJoin(t, ctx, firstDone, &joinedFirst)
			if second {
				refreshQueuedJoin(t, ctx, secondDone, &joinedSecond)
			}
			run(concurrentNew())
			fixture.MaterializedViewPopulation(run, "concurrent_mv", true)
			run(fixture.Exec("RESET lock_timeout", 0))
			run(fixture.Exec(concurrentRefreshSQL, 0))
		})
	}
}

func TestReferenceMaterializedRefreshConcurrentCancellation(t *testing.T) {
	for _, targetWait := range []bool{false, true} {
		t.Run(map[bool]string{false: "source_wait", true: "target_wait"}[targetWait], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					holder, first := fixture.LockConnections(t, p.Ctx, b.DB)
					worker, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer worker.Close()
					observer, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer observer.Close()
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, observer, b.Recorder, s) }
					concurrentSeed(run)
					concurrentIndex(run)
					concurrentChange(run)
					var before []byte
					if !b.PG {
						before = concurrentDump(t, p)
					}
					firstPID, workerPID, holderPID := 0, 0, 0
					var baseOID, targetOID int64
					if b.PG {
						firstPID = fixture.LockPID(t, p.Ctx, first)
						workerPID = fixture.LockPID(t, p.Ctx, worker)
						holderPID = fixture.LockPID(t, p.Ctx, holder)
						baseOID = fixture.LockOID(t, p.Ctx, "concurrent_base")
						targetOID = fixture.LockOID(t, p.Ctx, "concurrent_mv")
					}
					tx := fixture.QueryLockBegin(t, p.Ctx, holder)
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("LOCK TABLE concurrent_base IN ACCESS EXCLUSIVE MODE", 0))
					ctx, cancel := context.WithTimeout(p.Ctx, 15*time.Second)
					victimCtx, victimCancel := context.WithCancel(ctx)
					defer victimCancel()
					firstDone, victimDone := make(chan refreshQueuedOutcome, 1), make(chan refreshQueuedOutcome, 1)
					startedFirst, startedVictim, joinedFirst, joinedVictim := false, false, false, false
					defer func() {
						victimCancel()
						cancel()
						tx.Rollback()
						cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
						defer stop()
						for _, w := range []struct {
							started, joined bool
							done            <-chan refreshQueuedOutcome
						}{{startedFirst, joinedFirst, firstDone}, {startedVictim, joinedVictim, victimDone}} {
							if w.started && !w.joined {
								select {
								case <-w.done:
								case <-cleanup.Done():
									t.Error("cancel cleanup deadline", cleanup.Err())
								}
							}
						}
					}()
					if targetWait {
						startedFirst = true
						go refreshQueuedFirst(ctx, first, concurrentRefreshSQL, firstDone)
						if b.PG {
							fixture.LockWaitBehind(t, ctx, firstPID, holderPID, baseOID, "AccessShareLock")
						} else {
							concurrentNativeWait(t, ctx, "refreshQueuedFirst", "acquireBoundViewRelationModes", firstDone, &joinedFirst)
						}
					}
					stmt, err := worker.PrepareContext(p.Ctx, concurrentRefreshSQL)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					startedVictim = true
					go refreshQueuedSecond(victimCtx, stmt, victimDone)
					if b.PG {
						if targetWait {
							fixture.LockWaitBehind(t, ctx, workerPID, firstPID, targetOID, "ExclusiveLock")
						} else {
							fixture.LockWaitBehind(t, ctx, workerPID, holderPID, baseOID, "AccessShareLock")
						}
						var signaled bool
						if err := fixture.Admin.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", workerPID).Scan(&signaled); err != nil || !signaled {
							t.Fatal("backend cancel", signaled, err)
						}
					} else {
						acquisition := "acquireBoundViewRelationModes"
						if targetWait {
							acquisition = "acquireMaterializedRefresh"
						}
						concurrentNativeWait(t, ctx, "refreshQueuedSecond", acquisition, victimDone, &joinedVictim)
						victimCancel()
					}
					select {
					case o := <-victimDone:
						joinedVictim = true
						if o.result != nil {
							t.Fatal("canceled result nonnil")
						}
						if b.PG {
							d := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
							fixture.AssertDiagnostic(t, o.err, &d)
						} else if !errors.Is(o.err, context.Canceled) {
							t.Fatal("cancel error", o.err)
						}
					case <-ctx.Done():
						t.Fatal("cancel join", ctx.Err())
					}
					run(concurrentOld())
					fixture.MaterializedViewPopulation(run, "concurrent_mv", true)
					if !b.PG && !bytes.Equal(before, concurrentDump(t, p)) {
						t.Fatal("cancel changed Dump")
					}
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					if startedFirst {
						refreshQueuedJoin(t, ctx, firstDone, &joinedFirst)
					}
					run(fixture.Exec(concurrentRefreshSQL, 0))
					run(concurrentNew())
				})
			}
		})
	}
	// SQL timeout is independently asserted, not substituted for cancellation.
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name+"_timeout", func(t *testing.T) {
			holder, worker := fixture.LockConnections(t, p.Ctx, b.DB)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, worker, b.Recorder, s) }
			concurrentSeed(run)
			concurrentIndex(run)
			tx := fixture.QueryLockBegin(t, p.Ctx, holder)
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("LOCK TABLE concurrent_base IN ACCESS EXCLUSIVE MODE", 0))
			run(fixture.Exec("SET lock_timeout='100ms'", 0))
			run(fixture.ErrorDiagnostic(concurrentRefreshSQL, fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")))
			run(concurrentOld())
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(fixture.Exec(concurrentRefreshSQL, 0))
		})
	}
}
