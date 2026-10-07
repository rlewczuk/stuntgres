//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceTransactionIsolationSnapshotPhases(t *testing.T) {
	for _, isolation := range []string{"READ COMMITTED", "READ UNCOMMITTED", "REPEATABLE READ"} {
		for _, describe := range []bool{false, true} {
			t.Run(isolation+map[bool]string{false: "/execute", true: "/describe"}[describe], func(t *testing.T) {
				p := newReferencePair(t)
				ctx := p.ctx
				a, b := referenceLockConnections(t, ctx, p.postgres)
				referenceLockExec(t, ctx, b, "CREATE TABLE t(v INT)", 0)
				referenceLockExec(t, ctx, b, "INSERT INTO t VALUES(1)", 1)
				var pid int
				if err := a.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
					t.Fatal(err)
				}
				referenceLockExec(t, ctx, b, "BEGIN", 0)
				referenceLockExec(t, ctx, b, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
				referenceLockExec(t, ctx, a, "BEGIN ISOLATION LEVEL "+isolation, 0)
				type outcome struct {
					rows      *sql.Rows
					statement *sql.Stmt
					err       error
				}
				done := make(chan outcome, 1)
				go func() {
					var o outcome
					if describe {
						o.statement, o.err = a.PrepareContext(ctx, "SELECT v FROM t ORDER BY v")
					} else {
						o.rows, o.err = a.QueryContext(ctx, "SELECT v FROM t ORDER BY v")
					}
					done <- o
				}()
				referenceUpdateWait(t, ctx, pid)
				referenceLockExec(t, ctx, b, "INSERT INTO t VALUES(2)", 1)
				referenceLockExec(t, ctx, b, "COMMIT", 0)
				var o outcome
				select {
				case o = <-done:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				if o.err != nil {
					t.Fatal(o.err)
				}
				if describe {
					defer o.statement.Close()
					o.rows, o.err = o.statement.QueryContext(ctx)
					if o.err != nil {
						t.Fatal(o.err)
					}
				}
				want := []int64{1, 2}
				if isolation == "REPEATABLE READ" {
					want = []int64{1}
				}
				referenceTransactionValues(t, o.rows, want...)
				isolationReferenceSelect(t, ctx, a, "SELECT v FROM t ORDER BY v", false, want...)
				referenceLockExec(t, ctx, a, "ROLLBACK", 0)
			})
		}
	}
}
