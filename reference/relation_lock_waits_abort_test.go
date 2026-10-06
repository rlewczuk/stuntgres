//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"testing"
)

func TestReferenceRelationLockWaitsAbort(t *testing.T) {
	for _, prepare := range []bool{false, true} {
		name := "execute"
		if prepare {
			name = "prepare"
		}
		t.Run(name, func(t *testing.T) {
			p := newReferencePair(t)
			for index, db := range []*sql.DB{p.memory, p.postgres} {
				t.Run([]string{"native", "postgres"}[index], func(t *testing.T) {
					ctx := p.ctx
					a, b := referenceLockConnections(t, ctx, db)
					c, err := db.Conn(ctx)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { c.Close() })
					referenceLockExec(t, ctx, a, "CREATE TABLE t(v INT)", 0)
					referenceLockExec(t, ctx, a, "CREATE TABLE u(v INT)", 0)
					ap, bp := 0, 0
					if index == 1 {
						ap = referenceLockPID(t, ctx, a)
						bp = referenceLockPID(t, ctx, b)
					}
					referenceLockExec(t, ctx, b, "SET lock_timeout='200ms'", 0)
					holder, err := a.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { holder.Rollback() })
					referenceLockExec(t, ctx, holder, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
					waiter, err := b.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { waiter.Rollback() })
					referenceLockExec(t, ctx, waiter, "INSERT INTO u VALUES(9)", 1)
					await := referenceLockOperation(t, ctx, func(ctx context.Context) relationOutcome {
						if prepare {
							stmt, err := waiter.PrepareContext(ctx, "SELECT v FROM t")
							return relationOutcome{stmt: stmt, err: err}
						}
						rows, err := waiter.QueryContext(ctx, "SELECT v FROM t")
						return relationOutcome{rows: rows, err: err}
					})
					if index == 1 {
						referenceLockWaitBehind(t, ctx, bp, ap, "AccessShareLock")
					}
					o := await()
					if o.stmt != nil {
						o.stmt.Close()
						t.Fatal("failed Prepare returned handle")
					}
					if o.rows != nil {
						o.rows.Close()
						t.Fatal("failed Execute returned rows")
					}
					referenceWaitsDiagnostic(t, o.err, "55P03", "canceling statement due to lock timeout", "15")
					_, err = waiter.ExecContext(ctx, "SELECT v FROM u")
					referenceWaitsDiagnostic(t, err, "25P02", "current transaction is aborted, commands ignored until end of transaction block")
					// Before B rollback and while A remains a holder, C must
					// acquire B's prior resource and see no failed publication.
					peer, err := c.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer peer.Rollback()
					referenceLockExec(t, ctx, peer, "LOCK TABLE u IN ACCESS EXCLUSIVE MODE NOWAIT", 0)
					rows, err := peer.QueryContext(ctx, "SELECT v FROM u")
					if err != nil {
						t.Fatal(err)
					}
					referenceTransactionValues(t, rows)
					peer.Rollback()
					waiter.Rollback()
					holder.Rollback()
					referenceLockExec(t, ctx, b, "INSERT INTO t VALUES(2)", 1)
					rows, err = b.QueryContext(ctx, "SELECT v FROM t")
					if err != nil {
						t.Fatal(err)
					}
					referenceTransactionValues(t, rows, 2)
				})
			}
		})
	}
}
