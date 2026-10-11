package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRelationLockWaitsFairness(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		name := "cancel"
		if timeout {
			name = "timeout"
		}
		t.Run(name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					ctx := p.Ctx
					a, b := fixture.LockConnections(t, ctx, backend.DB)
					c, err := backend.DB.Conn(ctx)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { deadlockPublicRollback(c); c.Close() })
					fixture.LockExec(t, ctx, a, "CREATE TABLE t(v INT)", 0)
					fixture.LockExec(t, ctx, a, "INSERT INTO t VALUES(7)", 1)
					ap, bp, cp := 0, 0, 0
					if backend.PG {
						ap = fixture.LockPID(t, ctx, a)
						bp = fixture.LockPID(t, ctx, b)
						cp = fixture.LockPID(t, ctx, c)
					}
					if timeout {
						// Allow both PostgreSQL queue observations before AX
						// expires, without a full second per backend.
						fixture.LockExec(t, ctx, b, "SET lock_timeout='250ms'", 0)
					}
					holder, err := a.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { holder.Rollback() })
					rows, err := holder.QueryContext(ctx, "SELECT v FROM t")
					if err != nil {
						t.Fatal(err)
					}
					fixture.LimitLockValues(t, fixture.RelationOutcome{Rows: rows}, backend.PG, 7)
					waiter, err := b.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { waiter.Rollback() })
					waiting, cancel := context.WithCancel(ctx)
					defer cancel()
					ax := fixture.LockOperation(t, ctx, func(context.Context) fixture.RelationOutcome {
						r, e := waiter.ExecContext(waiting, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
						return fixture.RelationOutcome{Result: r, Err: e}
					})
					if backend.PG {
						fixture.LockWaitBehind(t, ctx, bp, ap, fixture.LockOID(t, ctx, "public.t"), "AccessExclusiveLock")
					}
					rx := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
						r, e := c.ExecContext(ctx, "INSERT INTO t VALUES(2)")
						return fixture.RelationOutcome{Result: r, Err: e}
					})
					if backend.PG {
						fixture.LockWaitBehind(t, ctx, cp, bp, fixture.LockOID(t, ctx, "public.t"), "RowExclusiveLock")
					}
					// PostgreSQL observer establishes both queue positions. The
					// paired native manager test establishes the same ordering
					// deterministically; no elapsed-time native queue assertion.
					if !timeout {
						if !backend.PG {
							cancel()
						} else {
							var canceled bool
							if err = fixture.Admin.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", bp).Scan(&canceled); err != nil || !canceled {
								t.Fatal(canceled, err)
							}
						}
					}
					failed := ax()
					if timeout {
						fixture.WaitsDiagnostic(t, failed.Err, "55P03", "canceling statement due to lock timeout")
					} else if !backend.PG {
						if !errors.Is(failed.Err, context.Canceled) {
							t.Fatal(failed.Err)
						}
					} else {
						fixture.WaitsDiagnostic(t, failed.Err, "57014", "canceling statement due to user request")
					}
					if failed.Result != nil {
						t.Fatal("failed AX wait returned result", failed.Result)
					}
					granted := rx()
					if granted.Err != nil {
						t.Fatal(granted.Err)
					}
					if n, e := granted.Result.RowsAffected(); e != nil || n != 1 {
						t.Fatal(n, e)
					}
					rows, err = holder.QueryContext(ctx, "SELECT v FROM t ORDER BY v")
					if err != nil {
						t.Fatal(err)
					}
					fixture.LimitLockValues(t, fixture.RelationOutcome{Rows: rows}, backend.PG, 2, 7)
					// Native database/sql can retire a transaction after caller
					// cancellation; SQL timeout and oracle admin cancellation
					// leave an explicit failed block with the portable 25P02.
					if timeout || backend.PG {
						_, err = waiter.ExecContext(ctx, "SELECT v FROM t")
						fixture.WaitsDiagnostic(t, err, "25P02", "current transaction is aborted, commands ignored until end of transaction block")
					}
					waiter.Rollback()
					holder.Rollback()
				})
			}
		})
	}
}
