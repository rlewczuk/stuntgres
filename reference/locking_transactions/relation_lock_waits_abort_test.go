package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRelationLockWaitsAbort(t *testing.T) {
	for _, prepare := range []bool{false, true} {
		name := "execute"
		if prepare {
			name = "prepare"
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
					t.Cleanup(func() { c.Close() })
					fixture.LockExec(t, ctx, a, "CREATE TABLE t(v INT)", 0)
					fixture.LockExec(t, ctx, a, "CREATE TABLE u(v INT)", 0)
					ap, bp := 0, 0
					if backend.PG {
						ap = fixture.LockPID(t, ctx, a)
						bp = fixture.LockPID(t, ctx, b)
					}
					fixture.LockExec(t, ctx, b, "SET lock_timeout='200ms'", 0)
					holder, err := a.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { holder.Rollback() })
					fixture.LockExec(t, ctx, holder, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
					waiter, err := b.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { waiter.Rollback() })
					fixture.LockExec(t, ctx, waiter, "INSERT INTO u VALUES(9)", 1)
					await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
						if prepare {
							stmt, err := waiter.PrepareContext(ctx, "SELECT v FROM t")
							return fixture.RelationOutcome{Stmt: stmt, Err: err}
						}
						rows, err := waiter.QueryContext(ctx, "SELECT v FROM t")
						return fixture.RelationOutcome{Rows: rows, Err: err}
					})
					if backend.PG {
						fixture.LockWaitBehind(t, ctx, bp, ap, fixture.LockOID(t, ctx, "public.t"), "AccessShareLock")
					}
					o := await()
					if o.Stmt != nil {
						o.Stmt.Close()
						t.Fatal("failed Prepare returned handle")
					}
					if o.Rows != nil {
						o.Rows.Close()
						t.Fatal("failed Execute returned rows")
					}
					fixture.WaitsDiagnostic(t, o.Err, "55P03", "canceling statement due to lock timeout", "15")
					_, err = waiter.ExecContext(ctx, "SELECT v FROM u")
					fixture.WaitsDiagnostic(t, err, "25P02", "current transaction is aborted, commands ignored until end of transaction block")
					// Before B rollback and while A remains a holder, C must
					// acquire B's prior resource and see no failed publication.
					peer, err := c.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer peer.Rollback()
					fixture.LockExec(t, ctx, peer, "LOCK TABLE u IN ACCESS EXCLUSIVE MODE NOWAIT", 0)
					rows, err := peer.QueryContext(ctx, "SELECT v FROM u")
					if err != nil {
						t.Fatal(err)
					}
					fixture.LimitLockValues(t, fixture.RelationOutcome{Rows: rows}, backend.PG)
					peer.Rollback()
					waiter.Rollback()
					holder.Rollback()
					fixture.LockExec(t, ctx, b, "INSERT INTO t VALUES(2)", 1)
					rows, err = b.QueryContext(ctx, "SELECT v FROM t")
					if err != nil {
						t.Fatal(err)
					}
					fixture.LimitLockValues(t, fixture.RelationOutcome{Rows: rows}, backend.PG, 2)
				})
			}
		})
	}
}
