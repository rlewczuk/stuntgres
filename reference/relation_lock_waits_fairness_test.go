package reference_test

import (
	"context"
	"errors"
	"testing"
)

func TestReferenceRelationLockWaitsFairness(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		name := "cancel"
		if timeout {
			name = "timeout"
		}
		t.Run(name, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					ctx := p.ctx
					a, b := referenceLockConnections(t, ctx, backend.db)
					c, err := backend.db.Conn(ctx)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { deadlockPublicRollback(c); c.Close() })
					referenceLockExec(t, ctx, a, "CREATE TABLE t(v INT)", 0)
					referenceLockExec(t, ctx, a, "INSERT INTO t VALUES(7)", 1)
					ap, bp, cp := 0, 0, 0
					if backend.pg {
						ap = referenceLockPID(t, ctx, a)
						bp = referenceLockPID(t, ctx, b)
						cp = referenceLockPID(t, ctx, c)
					}
					if timeout {
						// Allow both PostgreSQL queue observations before AX
						// expires, without a full second per backend.
						referenceLockExec(t, ctx, b, "SET lock_timeout='250ms'", 0)
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
					referenceLimitLockValues(t, relationOutcome{rows: rows}, backend.pg, 7)
					waiter, err := b.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { waiter.Rollback() })
					waiting, cancel := context.WithCancel(ctx)
					defer cancel()
					ax := referenceLockOperation(t, ctx, func(context.Context) relationOutcome {
						r, e := waiter.ExecContext(waiting, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
						return relationOutcome{result: r, err: e}
					})
					if backend.pg {
						referenceLockWaitBehind(t, ctx, bp, ap, referenceLockOID(t, ctx, "public.t"), "AccessExclusiveLock")
					}
					rx := referenceLockOperation(t, ctx, func(ctx context.Context) relationOutcome {
						r, e := c.ExecContext(ctx, "INSERT INTO t VALUES(2)")
						return relationOutcome{result: r, err: e}
					})
					if backend.pg {
						referenceLockWaitBehind(t, ctx, cp, bp, referenceLockOID(t, ctx, "public.t"), "RowExclusiveLock")
					}
					// PostgreSQL observer establishes both queue positions. The
					// paired native manager test establishes the same ordering
					// deterministically; no elapsed-time native queue assertion.
					if !timeout {
						if !backend.pg {
							cancel()
						} else {
							var canceled bool
							if err = referenceAdmin.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", bp).Scan(&canceled); err != nil || !canceled {
								t.Fatal(canceled, err)
							}
						}
					}
					failed := ax()
					if timeout {
						referenceWaitsDiagnostic(t, failed.err, "55P03", "canceling statement due to lock timeout")
					} else if !backend.pg {
						if !errors.Is(failed.err, context.Canceled) {
							t.Fatal(failed.err)
						}
					} else {
						referenceWaitsDiagnostic(t, failed.err, "57014", "canceling statement due to user request")
					}
					if failed.result != nil {
						t.Fatal("failed AX wait returned result", failed.result)
					}
					granted := rx()
					if granted.err != nil {
						t.Fatal(granted.err)
					}
					if n, e := granted.result.RowsAffected(); e != nil || n != 1 {
						t.Fatal(n, e)
					}
					rows, err = holder.QueryContext(ctx, "SELECT v FROM t ORDER BY v")
					if err != nil {
						t.Fatal(err)
					}
					referenceLimitLockValues(t, relationOutcome{rows: rows}, backend.pg, 2, 7)
					// Native database/sql can retire a transaction after caller
					// cancellation; SQL timeout and oracle admin cancellation
					// leave an explicit failed block with the portable 25P02.
					if timeout || backend.pg {
						_, err = waiter.ExecContext(ctx, "SELECT v FROM t")
						referenceWaitsDiagnostic(t, err, "25P02", "current transaction is aborted, commands ignored until end of transaction block")
					}
					waiter.Rollback()
					holder.Rollback()
				})
			}
		})
	}
}
