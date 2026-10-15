package reference_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBlockingPidsLifecycle(t *testing.T) {
	for _, safe := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary_cancel", true: "safe_cancel"}[safe], func(t *testing.T) {
			blockingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				holder, hid := blockingConn(t, p, b)
				waiter, wid := blockingConn(t, p, b)
				observer, _ := blockingConn(t, p, b)
				namespaceExec(t, p, b, holder, "CREATE TABLE blocking_lifecycle(v INT)")
				fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("INSERT INTO blocking_lifecycle VALUES(11)", 1))
				function := "pg_blocking_pids"
				if safe {
					function = "pg_safe_snapshot_blocking_pids"
					namespaceExec(t, p, b, holder, "BEGIN ISOLATION LEVEL SERIALIZABLE")
					fixture.PreparedReferenceRows(t, p, b, holder, "SELECT v FROM blocking_lifecycle", 11)
					namespaceExec(t, p, b, waiter, "BEGIN ISOLATION LEVEL SERIALIZABLE READ ONLY DEFERRABLE")
				} else {
					namespaceExec(t, p, b, holder, "BEGIN", "LOCK TABLE blocking_lifecycle IN ACCESS EXCLUSIVE MODE")
					namespaceExec(t, p, b, waiter, "BEGIN")
				}
				ctx, cancel := context.WithCancel(p.Ctx)
				defer cancel()
				await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
					if safe {
						rows, err := waiter.QueryContext(ctx, "SELECT v FROM blocking_lifecycle")
						return fixture.RelationOutcome{Rows: rows, Err: err}
					}
					r, err := waiter.ExecContext(ctx, "LOCK TABLE blocking_lifecycle IN ACCESS EXCLUSIVE MODE")
					return fixture.RelationOutcome{Result: r, Err: err}
				})
				blockingWait(t, p, b, observer, function, wid, hid)
				if b.PG {
					var ok bool
					if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", wid).Scan(&ok); err != nil || !ok {
						t.Fatal(ok, err)
					}
				} else {
					cancel()
				}
				o := await()
				if o.Rows != nil {
					o.Rows.Close()
					t.Fatal("canceled operation exposed rows")
				}
				if o.Result != nil || o.Stmt != nil {
					t.Fatal("canceled operation exposed result/handle")
				}
				if b.PG {
					fixture.WaitsDiagnostic(t, o.Err, "57014", "canceling statement due to user request")
				} else if !errors.Is(o.Err, context.Canceled) {
					t.Fatal("ordinary caller cancellation", o.Err)
				}
				blockingWant(t, p, b, observer, function, wid)
				// The original holder is still active; no release was used to clear
				// the canceled wait. The same physical waiter retains failed state.
				s := fixture.ErrorDiagnostic("SELECT v FROM blocking_lifecycle", fixture.TransactionAborted())
				s.SelectRows = true
				fixture.RunStep(t, p.Ctx, waiter, b.Recorder, s)
				namespaceExec(t, p, b, waiter, "ROLLBACK")
				namespaceExec(t, p, b, holder, "ROLLBACK")
				fixture.PreparedReferenceRows(t, p, b, waiter, "SELECT v FROM blocking_lifecycle", 11)
				blockingWant(t, p, b, observer, "pg_blocking_pids", wid)
				blockingWant(t, p, b, observer, "pg_safe_snapshot_blocking_pids", wid)
			})
		})
	}
	for _, safe := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary_owner_end", true: "safe_owner_end"}[safe], func(t *testing.T) {
			blockingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				holder, hid := blockingConn(t, p, b)
				waiter, wid := blockingConn(t, p, b)
				observer, _ := blockingConn(t, p, b)
				namespaceExec(t, p, b, holder, "CREATE TABLE blocking_safe(v INT)")
				fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("INSERT INTO blocking_safe VALUES(11)", 1))
				function := "pg_blocking_pids"
				var await func() fixture.RelationOutcome
				if safe {
					function = "pg_safe_snapshot_blocking_pids"
					namespaceExec(t, p, b, holder, "BEGIN ISOLATION LEVEL SERIALIZABLE")
					fixture.PreparedReferenceRows(t, p, b, holder, "SELECT v FROM blocking_safe", 11)
					namespaceExec(t, p, b, waiter, "BEGIN ISOLATION LEVEL SERIALIZABLE READ ONLY DEFERRABLE")
					await = blockingSafeOperation(t, p, waiter, false)
				} else {
					namespaceExec(t, p, b, holder, "BEGIN", "LOCK TABLE blocking_safe IN ACCESS EXCLUSIVE MODE")
					namespaceExec(t, p, b, waiter, "BEGIN")
					await = blockingLock(t, p, waiter, "LOCK TABLE blocking_safe IN ACCESS EXCLUSIVE MODE", false)
				}
				blockingWait(t, p, b, observer, function, wid, hid)
				if err := holder.Raw(func(any) error { return driver.ErrBadConn }); err != driver.ErrBadConn {
					t.Fatal(err)
				}
				if safe {
					blockingSafeDone(t, p, await, false)
				} else {
					blockingLockDone(t, await)
				}
				blockingWant(t, p, b, observer, function, wid)
				namespaceExec(t, p, b, waiter, "ROLLBACK")
			})
		})
	}
	for _, safe := range []bool{false, true} {
		for _, restore := range []bool{false, true} {
			t.Run("native_epoch/"+map[bool]string{false: "ordinary/", true: "safe/"}[safe]+map[bool]string{false: "reset", true: "restore"}[restore], func(t *testing.T) {
				blockingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					if b.PG {
						return
					} // Native replacement has no PostgreSQL API counterpart.
					holder, hid := blockingConn(t, p, b)
					waiter, wid := blockingConn(t, p, b)
					observer, _ := blockingConn(t, p, b)
					namespaceExec(t, p, b, holder, "CREATE TABLE blocking_safe(v INT)")
					fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("INSERT INTO blocking_safe VALUES(11)", 1))
					function := "pg_blocking_pids"
					var await func() fixture.RelationOutcome
					if safe {
						function = "pg_safe_snapshot_blocking_pids"
						namespaceExec(t, p, b, holder, "BEGIN ISOLATION LEVEL SERIALIZABLE")
						fixture.PreparedReferenceRows(t, p, b, holder, "SELECT v FROM blocking_safe", 11)
						namespaceExec(t, p, b, waiter, "BEGIN ISOLATION LEVEL SERIALIZABLE READ ONLY DEFERRABLE")
						await = blockingSafeOperation(t, p, waiter, false)
					} else {
						namespaceExec(t, p, b, holder, "BEGIN", "LOCK TABLE blocking_safe IN ACCESS EXCLUSIVE MODE")
						namespaceExec(t, p, b, waiter, "BEGIN")
						await = blockingLock(t, p, waiter, "LOCK TABLE blocking_safe IN ACCESS EXCLUSIVE MODE", false)
					}
					blockingWait(t, p, b, observer, function, wid, hid)
					var err error
					if restore {
						err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE blocking_after(v INT); INSERT INTO blocking_after VALUES(23);"))
					} else {
						err = stuntgres.Reset(p.Ctx, p.Native)
					}
					if err != nil {
						t.Fatal(err)
					}
					o := await()
					if o.Rows != nil {
						o.Rows.Close()
						t.Fatal("replaced wait exposed rows")
					}
					fixture.TransactionOrdinary(t, o.Err, "stuntgres: catalog replaced before publication")
					namespaceExec(t, p, b, holder, "ROLLBACK")
					namespaceExec(t, p, b, waiter, "ROLLBACK")
					blockingWant(t, p, b, observer, "pg_blocking_pids", wid)
					blockingWant(t, p, b, observer, "pg_safe_snapshot_blocking_pids", wid)
					if restore {
						fixture.PreparedReferenceRows(t, p, b, observer, "SELECT v FROM blocking_after", 23)
					} else {
						namespaceExec(t, p, b, observer, "CREATE TABLE blocking_after(v INT)")
					}
				})
			})
		}
	}
}
