package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalTriggerLocks(t *testing.T) {
	t.Run("physical-close", transactionalTriggerPhysicalClose)
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprintf("end/commit=%v", commit), func(t *testing.T) {
			transactionalTriggerLockEnd(t, commit)
		})
	}
	for _, partition := range []bool{false, true} {
		for _, replace := range []bool{false, true} {
			for _, held := range []bool{false, true} {
				t.Run(fmt.Sprintf("partition=%v/replace=%v/held=%v", partition, replace, held), func(t *testing.T) {
					triggerDDLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						target, blocked := "tr_data", "tr_data"
						if partition {
							triggerDDLPartitionSeed(t, p, b, c)
							target, blocked = "tr_parent", "tr_sibling"
						} else {
							triggerSetup(t, p, b, c)
							if replace {
								userOperatorExec(t, p, b, c, "CREATE TRIGGER a BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_add(3)")
							}
						}
						prefix, name := "CREATE TRIGGER ", "b"
						if replace {
							prefix, name = "CREATE OR REPLACE TRIGGER ", "a"
						}
						q := prefix + name + " BEFORE INSERT ON " + target + " FOR EACH ROW EXECUTE FUNCTION tr_add(7)"
						var stmt *sql.Stmt
						var err error
						if held {
							stmt, err = c.PrepareContext(p.Ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							defer stmt.Close()
						}
						holder, err := b.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer holder.Close()
						defer holder.ExecContext(context.Background(), "ROLLBACK")
						defer c.ExecContext(context.Background(), "ROLLBACK")
						waiterPID, holderPID := 0, 0
						var blockedOID int64
						if b.PG {
							waiterPID = fixture.LockPID(t, p.Ctx, c)
							holderPID = fixture.LockPID(t, p.Ctx, holder)
							blockedOID = fixture.LockOID(t, p.Ctx, blocked)
						}
						fixture.LockExec(t, p.Ctx, holder, "BEGIN", 0)
						fixture.LockExec(t, p.Ctx, holder, "INSERT INTO "+blocked+" VALUES(51,5)", 1)
						fixture.LockExec(t, p.Ctx, c, "BEGIN", 0)
						fixture.LockExec(t, p.Ctx, c, "SAVEPOINT admission", 0)
						fixture.BinaryConversionActiveCancelAfter(t, p, b, c, q, "(*relationRequest).wait", func(ctx context.Context) {
							if b.PG {
								fixture.LockWaitBehind(t, ctx, waiterPID, holderPID, blockedOID, "ShareRowExclusiveLock")
							}
						}, func(ctx context.Context) fixture.RelationOutcome {
							var r sql.Result
							var err error
							if held {
								r, err = stmt.ExecContext(ctx)
							} else {
								r, err = c.ExecContext(ctx, q)
							}
							return fixture.RelationOutcome{Result: r, Err: err}
						})
						fixture.RunStep(t, p.Ctx, c, b.Recorder, triggerDDLError("SELECT 1", "25P02", "current transaction is aborted, commands ignored until end of transaction block", false))
						fixture.LockExec(t, p.Ctx, c, "ROLLBACK TO admission", 0)
						fixture.LockExec(t, p.Ctx, holder, "ROLLBACK", 0)
						var execute fixture.Executor = c
						if held {
							execute = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
						}
						fixture.RunStep(t, p.Ctx, execute, b.Recorder, fixture.Exec(q, 0))
						want := int64(12)
						if partition && !replace {
							want = 15
						}
						fixture.RunStep(t, p.Ctx, c, b.Recorder, triggerRows("INSERT INTO "+target+" VALUES(51,5) RETURNING id,v", []any{int64(51), want}))
						fixture.LockExec(t, p.Ctx, c, "ROLLBACK TO admission", 0)
						// The postmark CREATE grant was released; this peer writer must not wait.
						fixture.LockExec(t, p.Ctx, holder, "INSERT INTO "+blocked+" VALUES(52,5)", 1)
						fixture.LockExec(t, p.Ctx, c, "RELEASE admission", 0)
						fixture.LockExec(t, p.Ctx, c, "COMMIT", 0)
						old := int64(5)
						if partition || replace {
							old = 8
						}
						fixture.RunStep(t, p.Ctx, holder, b.Recorder, triggerRows("SELECT id,v FROM "+blocked, []any{int64(52), old}))
					})
				})
			}
		}
	}
	t.Run("lock-timeout", func(t *testing.T) {
		triggerDDLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			triggerSetup(t, p, b, c)
			holder, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Close()
			defer holder.ExecContext(context.Background(), "ROLLBACK")
			q := "CREATE TRIGGER a BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_double()"
			held := triggerDDLHeld(t, p, b, c, q)
			fixture.LockExec(t, p.Ctx, holder, "BEGIN", 0)
			fixture.LockExec(t, p.Ctx, holder, "INSERT INTO tr_data VALUES(1,5)", 1)
			fixture.LockExec(t, p.Ctx, c, "BEGIN", 0)
			defer c.ExecContext(context.Background(), "ROLLBACK")
			fixture.LockExec(t, p.Ctx, c, "SET LOCAL lock_timeout='100ms'", 0)
			held(triggerDDLError(q, "55P03", "canceling statement due to lock timeout", false))
			fixture.LockExec(t, p.Ctx, c, "ROLLBACK", 0)
			fixture.LockExec(t, p.Ctx, holder, "COMMIT", 0)
			fixture.RunStep(t, p.Ctx, holder, b.Recorder, triggerRows("SELECT id,v FROM tr_data", []any{int64(1), int64(5)}))
		})
	})
}

func transactionalTriggerPhysicalClose(t *testing.T) {
	triggerDDLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		triggerSetup(t, p, b, c)
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		waiter, holder := 0, 0
		var oid int64
		if b.PG {
			waiter = fixture.LockPID(t, p.Ctx, peer)
			holder = fixture.LockPID(t, p.Ctx, c)
			oid = fixture.LockOID(t, p.Ctx, "tr_data")
		}
		fixture.LockExec(t, p.Ctx, c, "BEGIN", 0)
		fixture.LockExec(t, p.Ctx, c, "CREATE TRIGGER a BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_add(7)", 0)
		fixture.LockExec(t, p.Ctx, c, "INSERT INTO tr_data VALUES(1,5)", 1)
		done := make(chan struct{})
		await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
			defer close(done)
			result, err := peer.ExecContext(ctx, "CREATE TRIGGER z BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_double()")
			return fixture.RelationOutcome{Result: result, Err: err}
		})
		fixture.RelationLockPending(t, p, b, waiter, holder, oid, "ShareRowExclusiveLock", done)
		err = c.Raw(func(raw any) error {
			if err := raw.(driver.Conn).Close(); err != nil {
				return err
			}
			return driver.ErrBadConn
		})
		if !errors.Is(err, driver.ErrBadConn) {
			t.Fatal("physical close", err)
		}
		out := await()
		if out.Err != nil {
			t.Fatal(out.Err)
		}
		count, err := out.Result.RowsAffected()
		if err != nil || count != 0 {
			t.Fatal(count, err)
		}
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, triggerRows("SELECT id,v FROM tr_data"))
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, triggerRows("INSERT INTO tr_data VALUES(2,5) RETURNING id,v", []any{int64(2), int64(10)}))
	})
}

func transactionalTriggerLockEnd(t *testing.T, commit bool) {
	triggerDDLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		triggerSetup(t, p, b, c)
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		defer c.ExecContext(context.Background(), "ROLLBACK")
		waiter, holder := 0, 0
		var oid int64
		if b.PG {
			waiter = fixture.LockPID(t, p.Ctx, peer)
			holder = fixture.LockPID(t, p.Ctx, c)
			oid = fixture.LockOID(t, p.Ctx, "tr_data")
		}
		fixture.LockExec(t, p.Ctx, c, "BEGIN", 0)
		fixture.LockExec(t, p.Ctx, c, "SAVEPOINT claims", 0)
		fixture.LockExec(t, p.Ctx, c, "CREATE TRIGGER a BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_add(3)", 0)
		fixture.LockExec(t, p.Ctx, c, "INSERT INTO tr_data VALUES(1,5)", 1)
		fixture.LockExec(t, p.Ctx, c, "RELEASE claims", 0)
		done := make(chan struct{})
		await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
			defer close(done)
			rows, err := peer.QueryContext(ctx, "INSERT INTO tr_data VALUES(2,5) RETURNING v")
			return fixture.RelationOutcome{Rows: rows, Err: err}
		})
		fixture.RelationLockPending(t, p, b, waiter, holder, oid, "RowExclusiveLock", done)
		end, want := "ROLLBACK", int64(5)
		if commit {
			end, want = "COMMIT", 8
		}
		fixture.LockExec(t, p.Ctx, c, end, 0)
		fixture.LimitLockValues(t, await(), b.PG, want)
		rows := triggerRows("SELECT id,v FROM tr_data ORDER BY id", []any{int64(2), want})
		if commit {
			rows = triggerRows(rows.Query, []any{int64(1), int64(8)}, []any{int64(2), int64(8)})
		}
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, rows)
	})
}
