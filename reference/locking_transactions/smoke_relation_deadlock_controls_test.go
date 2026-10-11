package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func smokeDeadlockAcyclic(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
			defer cancel()
			a, b := fixture.LockConnections(t, ctx, backend.DB)
			c := deadlockPublicConn(t, ctx, backend.DB)
			deadlockPublicExec(t, ctx, a, "CREATE TABLE t(v INT)", 0)
			deadlockPublicExec(t, ctx, a, "CREATE TABLE u(v INT)", 0)
			ap, bp, cp := 0, 0, 0
			var x, y int64
			if backend.PG {
				ap, bp, cp = fixture.LockPID(t, ctx, a), fixture.LockPID(t, ctx, b), fixture.LockPID(t, ctx, c)
				x, y = fixture.LockOID(t, ctx, "public.t"), fixture.LockOID(t, ctx, "public.u")
			}
			for _, conn := range []*sql.Conn{a, b, c} {
				deadlockPublicExec(t, ctx, conn, "BEGIN", 0)
				t.Cleanup(func() { deadlockPublicRollback(conn) })
			}
			deadlockPublicExec(t, ctx, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
			deadlockPublicExec(t, ctx, b, "LOCK TABLE u IN ACCESS EXCLUSIVE MODE", 0)
			rb := deadlockPublicWorker(t, ctx, 1, func(ctx context.Context) (sql.Result, error) {
				return b.ExecContext(ctx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
			})
			if backend.PG {
				fixture.LockWaitBehind(t, ctx, bp, ap, x, "AccessExclusiveLock")
			}
			rc := deadlockPublicWorker(t, ctx, 2, func(ctx context.Context) (sql.Result, error) {
				return c.ExecContext(ctx, "LOCK TABLE u IN ACCESS EXCLUSIVE MODE")
			})
			if backend.PG {
				fixture.LockWaitBehind(t, ctx, cp, bp, y, "AccessExclusiveLock")
			}
			deadlockPublicExec(t, ctx, a, "COMMIT", 0)
			o := rb()
			if o.err != nil {
				t.Fatal("acyclic waiter B", o.err)
			}
			deadlockPublicCount(t, o.result, 0)
			deadlockPublicExec(t, ctx, b, "ROLLBACK", 0)
			o = rc()
			if o.err != nil {
				t.Fatal("acyclic waiter C", o.err)
			}
			deadlockPublicCount(t, o.result, 0)
			deadlockPublicExec(t, ctx, c, "COMMIT", 0)
		})
	}
}
