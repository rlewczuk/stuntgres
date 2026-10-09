package reference_test

import (
	"context"
	"testing"
)

func referenceIsolationSnapshotPhases(t *testing.T) {
	t.Helper()
	for _, isolation := range []string{"READ COMMITTED", "READ UNCOMMITTED", "REPEATABLE READ"} {
		for _, describe := range []bool{false, true} {
			t.Run(isolation+map[bool]string{false: "/execute", true: "/describe"}[describe], func(t *testing.T) {
				p := newReferenceScenario(t)
				ctx := p.ctx
				a, b := referenceLockConnections(t, ctx, p.postgres)
				referenceLockExec(t, ctx, b, "CREATE TABLE t(v INT)", 0)
				referenceLockExec(t, ctx, b, "INSERT INTO t VALUES(1)", 1)
				ap, bp := referenceRowPIDs(t, ctx, a, b)
				oid := referenceLockOID(t, ctx, "t")
				referenceLockExec(t, ctx, b, "BEGIN", 0)
				referenceLockExec(t, ctx, b, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
				referenceLockExec(t, ctx, a, "BEGIN ISOLATION LEVEL "+isolation, 0)
				await := referenceLockOperation(t, ctx, func(ctx context.Context) relationOutcome {
					var o relationOutcome
					if describe {
						o.stmt, o.err = a.PrepareContext(ctx, "SELECT v FROM t ORDER BY v")
					} else {
						o.rows, o.err = a.QueryContext(ctx, "SELECT v FROM t ORDER BY v")
					}
					return o
				})
				referenceLockWaitBehind(t, ctx, ap, bp, oid, "AccessShareLock")
				referenceLockExec(t, ctx, b, "INSERT INTO t VALUES(2)", 1)
				referenceLockExec(t, ctx, b, "COMMIT", 0)
				o := await()
				if o.err != nil {
					t.Fatal(o.err)
				}
				if describe {
					defer o.stmt.Close()
					o.rows, o.err = o.stmt.QueryContext(ctx)
					if o.err != nil {
						t.Fatal(o.err)
					}
				}
				want := []int64{1, 2}
				if isolation == "REPEATABLE READ" {
					want = []int64{1}
				}
				referenceLimitLockValues(t, o, true, want...)
				isolationReferenceSelect(t, ctx, a, true, "SELECT v FROM t ORDER BY v", false, want...)
				referenceLockExec(t, ctx, a, "ROLLBACK", 0)
			})
		}
	}
}
