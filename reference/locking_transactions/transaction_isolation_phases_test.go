package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func referenceIsolationSnapshotPhases(t *testing.T) {
	t.Helper()
	for _, isolation := range []string{"READ COMMITTED", "READ UNCOMMITTED", "REPEATABLE READ"} {
		for _, describe := range []bool{false, true} {
			t.Run(isolation+map[bool]string{false: "/execute", true: "/describe"}[describe], func(t *testing.T) {
				p := fixture.NewScenario(t)
				ctx := p.Ctx
				a, b := fixture.LockConnections(t, ctx, p.Postgres)
				fixture.LockExec(t, ctx, b, "CREATE TABLE t(v INT)", 0)
				fixture.LockExec(t, ctx, b, "INSERT INTO t VALUES(1)", 1)
				ap, bp := fixture.RowPIDs(t, ctx, a, b)
				oid := fixture.LockOID(t, ctx, "t")
				fixture.LockExec(t, ctx, b, "BEGIN", 0)
				fixture.LockExec(t, ctx, b, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
				fixture.LockExec(t, ctx, a, "BEGIN ISOLATION LEVEL "+isolation, 0)
				await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
					var o fixture.RelationOutcome
					if describe {
						o.Stmt, o.Err = a.PrepareContext(ctx, "SELECT v FROM t ORDER BY v")
					} else {
						o.Rows, o.Err = a.QueryContext(ctx, "SELECT v FROM t ORDER BY v")
					}
					return o
				})
				fixture.LockWaitBehind(t, ctx, ap, bp, oid, "AccessShareLock")
				fixture.LockExec(t, ctx, b, "INSERT INTO t VALUES(2)", 1)
				fixture.LockExec(t, ctx, b, "COMMIT", 0)
				o := await()
				if o.Err != nil {
					t.Fatal(o.Err)
				}
				if describe {
					defer o.Stmt.Close()
					o.Rows, o.Err = o.Stmt.QueryContext(ctx)
					if o.Err != nil {
						t.Fatal(o.Err)
					}
				}
				want := []int64{1, 2}
				if isolation == "REPEATABLE READ" {
					want = []int64{1}
				}
				fixture.LimitLockValues(t, o, true, want...)
				fixture.IsolationReferenceSelect(t, ctx, a, true, "SELECT v FROM t ORDER BY v", false, want...)
				fixture.LockExec(t, ctx, a, "ROLLBACK", 0)
			})
		}
	}
}
