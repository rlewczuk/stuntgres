package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"runtime"
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

func TestReferenceBlockingPidsDatabaseStartup(t *testing.T) {
	for _, release := range []string{"COMMIT", "ROLLBACK"} {
		t.Run(release, func(t *testing.T) {
			const database = "blocking_pids_startup"
			const privateName = "blocking_pids_startup_private"
			names := []string{database, privateName}
			fixture.DatabaseReferenceCleanup(t, &names)
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					holder, holderPID := blockingConn(t, p, b)
					observer, _ := blockingConn(t, p, b)
					namespaceExec(t, p, b, holder, "CREATE DATABASE "+database)
					var oid int64
					if err := observer.QueryRowContext(p.Ctx, "SELECT oid FROM pg_database WHERE datname=$1", database).Scan(&oid); err != nil {
						t.Fatal(err)
					}
					fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Select(
						"SELECT current_database()=$1 AS selected", []string{"selected"}, []string{"BOOL"}, [][]any{{false}}, database))
					pool, _, recorder := fixture.DatabaseScenarioPool(t, p, b.PG, database, "")
					pool.SetMaxIdleConns(0) // Returning the worker must physically disconnect it.
					// A round-trip rename retains the real database AX until
					// transaction end, without changing the startup selector.
					namespaceExec(t, p, b, holder, "BEGIN",
						"ALTER DATABASE "+database+" RENAME TO "+privateName,
						"ALTER DATABASE "+privateName+" RENAME TO "+database)
					fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Select(
						`SELECT mode,granted FROM pg_locks WHERE pid=$1 AND locktype='object' AND classid='pg_database'::regclass::oid AND objid=$2::oid AND objsubid=0 AND mode='AccessExclusiveLock'`,
						[]string{"mode", "granted"}, []string{"TEXT", "BOOL"}, [][]any{{"AccessExclusiveLock", true}}, holderPID, oid))

					type startupResult struct {
						conn *sql.Conn
						err  error
					}
					ctx, cancel := context.WithTimeout(p.Ctx, fixture.Liveness)
					defer cancel()
					result := make(chan startupResult, 1)
					finished := make(chan struct{})
					go func() {
						c, err := pool.Conn(ctx)
						result <- startupResult{c, err}
						close(finished)
					}()
					joined := false
					defer func() {
						// Startup transports need not honor cancellation during login.
						// Release the actual holder before joining, on every exit.
						cleanup, stop := context.WithTimeout(context.Background(), fixture.Liveness)
						defer stop()
						if _, err := holder.ExecContext(cleanup, "ROLLBACK"); err != nil {
							t.Error("release startup blocker", err)
						}
						cancel()
						if !joined {
							r := <-result
							<-finished
							if r.conn != nil {
								_ = r.conn.Close()
							}
						}
					}()
					var waiterPID int
					for {
						select {
						case <-finished:
							t.Fatal("physical startup completed before its required database lock wait")
						case <-ctx.Done():
							t.Fatal("physical startup admission", ctx.Err())
						default:
						}
						// Observe the real shared object wait independently of the
						// blocking function under test, from the other database.
						err := observer.QueryRowContext(ctx,
							`SELECT pid FROM pg_locks WHERE locktype='object' AND classid='pg_database'::regclass::oid AND objid=$1::oid AND objsubid=0 AND mode='RowExclusiveLock' AND NOT granted`,
							oid).Scan(&waiterPID)
						if err == nil {
							break
						}
						if !errors.Is(err, sql.ErrNoRows) {
							t.Fatal("startup lock observation", err)
						}
						runtime.Gosched()
					}
					if waiterPID <= 0 || waiterPID == holderPID {
						t.Fatal("invalid physical startup lock identity", waiterPID, holderPID)
					}
					blockingWant(t, p, b, observer, "pg_blocking_pids", waiterPID, holderPID)
					query := "SELECT pg_blocking_pids($1)"
					statement, err := observer.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer statement.Close()
					held := fixture.HeldStatement{Executor: observer, Statement: statement, SQL: query}
					blockingWant(t, p, b, held, "pg_blocking_pids", waiterPID, holderPID)
					blockingWant(t, p, b, held, "pg_blocking_pids", waiterPID, holderPID)
					namespaceExec(t, p, b, holder, release)
					r := <-result
					<-finished
					joined = true
					if r.conn != nil {
						defer r.conn.Close()
					}
					if r.err != nil || r.conn == nil {
						t.Fatal("released physical startup", r.err)
					}
					fixture.RunStep(t, p.Ctx, r.conn, recorder, fixture.Select(
						"SELECT current_database()", []string{"current_database"}, []string{"NAME"}, [][]any{{database}}))
					blockingWant(t, p, b, observer, "pg_blocking_pids", waiterPID)
					blockingWant(t, p, b, held, "pg_blocking_pids", waiterPID)
					fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Select(
						`SELECT count(*) FROM pg_locks WHERE pid=$1 AND locktype='object' AND classid='pg_database'::regclass::oid AND objid=$2::oid AND mode='RowExclusiveLock' AND NOT granted`,
						[]string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, waiterPID, oid))
					fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Select(
						"SELECT datname FROM pg_database WHERE oid=$1::oid", []string{"datname"}, []string{"NAME"}, [][]any{{database}}, oid))
					fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Select(
						"SELECT count(*) FROM pg_database WHERE datname=$1", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, privateName))
				})
			}
		})
	}
}
