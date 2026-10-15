package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBlockingPidsHardSoft(t *testing.T) {
	for _, release := range []string{"COMMIT", "ROLLBACK"} {
		for _, prepared := range []bool{false, true} {
			t.Run(release+map[bool]string{false: "/direct", true: "/held"}[prepared], func(t *testing.T) {
				blockingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					a, aid := blockingConn(t, p, b)
					c, cid := blockingConn(t, p, b)
					observer, _ := blockingConn(t, p, b)
					namespaceExec(t, p, b, a, "CREATE TABLE blocking_hard(v INT)")
					// Prepare utility before the holder takes its lock; execute must wait.
					var execute func() fixture.RelationOutcome
					namespaceExec(t, p, b, c, "BEGIN")
					if prepared {
						s, err := c.PrepareContext(p.Ctx, "LOCK TABLE blocking_hard IN ACCESS EXCLUSIVE MODE")
						if err != nil {
							t.Fatal(err)
						}
						// Registered before the worker: cancel/join must precede
						// Close, which database/sql otherwise waits on in-flight.
						t.Cleanup(func() { _ = s.Close() })
						namespaceExec(t, p, b, a, "BEGIN", "LOCK TABLE blocking_hard IN ACCESS EXCLUSIVE MODE")
						execute = fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
							r, err := s.ExecContext(ctx)
							return fixture.RelationOutcome{Result: r, Err: err}
						})
					} else {
						namespaceExec(t, p, b, a, "BEGIN", "LOCK TABLE blocking_hard IN ACCESS EXCLUSIVE MODE")
						execute = blockingLock(t, p, c, "LOCK TABLE blocking_hard IN ACCESS EXCLUSIVE MODE", false)
					}
					q := "SELECT pg_blocking_pids($1)"
					held, err := observer.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					x := fixture.HeldStatement{Executor: observer, Statement: held, SQL: q}
					blockingWait(t, p, b, x, "pg_blocking_pids", cid, aid)
					blockingWant(t, p, b, observer, "pg_safe_snapshot_blocking_pids", cid)
					namespaceExec(t, p, b, a, release)
					blockingLockDone(t, execute)
					blockingWant(t, p, b, x, "pg_blocking_pids", cid)
					namespaceExec(t, p, b, c, "ROLLBACK")
				})
			})
		}
	}
	t.Run("actual_prepare", func(t *testing.T) {
		blockingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			a, aid := blockingConn(t, p, b)
			waiter, wid := blockingConn(t, p, b)
			observer, _ := blockingConn(t, p, b)
			namespaceExec(t, p, b, a, "CREATE TABLE blocking_safe(v INT)")
			fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Exec("INSERT INTO blocking_safe VALUES(11)", 1))
			namespaceExec(t, p, b, a, "BEGIN", "LOCK TABLE blocking_safe IN ACCESS EXCLUSIVE MODE")
			namespaceExec(t, p, b, waiter, "BEGIN")
			await := blockingSafeOperation(t, p, waiter, true)
			blockingWait(t, p, b, observer, "pg_blocking_pids", wid, aid)
			blockingWant(t, p, b, observer, "pg_safe_snapshot_blocking_pids", wid)
			namespaceExec(t, p, b, a, "COMMIT")
			blockingSafeDone(t, p, await, true)
			blockingWant(t, p, b, observer, "pg_blocking_pids", wid)
			namespaceExec(t, p, b, waiter, "ROLLBACK")
		})
	})
	t.Run("soft_queue", func(t *testing.T) {
		blockingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			a, aid := blockingConn(t, p, b)
			middle, mid := blockingConn(t, p, b)
			tail, tid := blockingConn(t, p, b)
			observer, _ := blockingConn(t, p, b)
			namespaceExec(t, p, b, a, "CREATE TABLE blocking_soft(v INT)", "BEGIN", "LOCK TABLE blocking_soft IN ACCESS SHARE MODE")
			namespaceExec(t, p, b, middle, "BEGIN")
			ctx, cancel := context.WithCancel(p.Ctx)
			defer cancel()
			awaitMiddle := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
				r, err := middle.ExecContext(ctx, "LOCK TABLE blocking_soft IN ACCESS EXCLUSIVE MODE")
				return fixture.RelationOutcome{Result: r, Err: err}
			})
			blockingWait(t, p, b, observer, "pg_blocking_pids", mid, aid)
			namespaceExec(t, p, b, tail, "BEGIN")
			awaitTail := blockingLock(t, p, tail, "LOCK TABLE blocking_soft IN ACCESS SHARE MODE", false)
			blockingWait(t, p, b, observer, "pg_blocking_pids", tid, mid)
			blockingWant(t, p, b, observer, "pg_safe_snapshot_blocking_pids", tid)
			if b.PG {
				var ok bool
				if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", mid).Scan(&ok); err != nil || !ok {
					t.Fatal(ok, err)
				}
			} else {
				cancel()
			}
			o := awaitMiddle()
			if o.Err == nil {
				t.Fatal("queued AX cancellation succeeded")
			}
			if b.PG {
				fixture.WaitsDiagnostic(t, o.Err, "57014", "canceling statement due to user request")
			}
			blockingLockDone(t, awaitTail)
			blockingWant(t, p, b, observer, "pg_blocking_pids", tid)
			// Compatible A remains in its original transaction throughout.
			namespaceExec(t, p, b, a, "ROLLBACK")
			namespaceExec(t, p, b, middle, "ROLLBACK")
			namespaceExec(t, p, b, tail, "ROLLBACK")
		})
	})
}

func TestReferenceBlockingPidsPrepared(t *testing.T) {
	blockingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, _ := blockingConn(t, p, b)
		second, _ := blockingConn(t, p, b)
		waiter, wid := blockingConn(t, p, b)
		observer, _ := blockingConn(t, p, b)
		namespaceExec(t, p, b, a, "CREATE TABLE blocking_prepared(v INT)")
		for i, c := range []*sql.Conn{a, second} {
			gid := []string{"blocking_pids_first", "blocking_pids_second"}[i]
			namespaceExec(t, p, b, c, "BEGIN", "LOCK TABLE blocking_prepared IN ACCESS SHARE MODE")
			fixture.PreparedReferencePrepare(t, p, b, c, gid)
		}
		// A new transaction in an origin is not the detached owner.
		namespaceExec(t, p, b, a, "BEGIN", "ROLLBACK")
		if err := second.Raw(func(any) error { return driver.ErrBadConn }); err != driver.ErrBadConn {
			t.Fatal(err)
		}
		namespaceExec(t, p, b, waiter, "BEGIN")
		await := blockingLock(t, p, waiter, "LOCK TABLE blocking_prepared IN ACCESS EXCLUSIVE MODE", false)
		blockingWait(t, p, b, observer, "pg_blocking_pids", wid, 0, 0)
		blockingWant(t, p, b, observer, "pg_safe_snapshot_blocking_pids", wid)
		namespaceExec(t, p, b, observer, "COMMIT PREPARED 'blocking_pids_first'")
		blockingWait(t, p, b, observer, "pg_blocking_pids", wid, 0)
		namespaceExec(t, p, b, observer, "ROLLBACK PREPARED 'blocking_pids_second'")
		blockingLockDone(t, await)
		blockingWant(t, p, b, observer, "pg_blocking_pids", wid)
		namespaceExec(t, p, b, waiter, "ROLLBACK")
	})
}
