package smoke_test

import (
	"context"
	"database/sql"
	"testing"
	"time"
)


func deadlockPublicAcyclic(t *testing.T) {
	db := deadlockPublicDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	a := deadlockPublicConn(t, ctx, db)
	b := deadlockPublicConn(t, ctx, db)
	c := deadlockPublicConn(t, ctx, db)
	deadlockPublicExec(t, ctx, a, "CREATE TABLE t(v INT)", 0)
	deadlockPublicExec(t, ctx, a, "CREATE TABLE u(v INT)", 0)
	for _, conn := range []*sql.Conn{a, b, c} {
		deadlockPublicExec(t, ctx, conn, "BEGIN", 0)
		t.Cleanup(func() { deadlockPublicRollback(conn) })
	}
	deadlockPublicExec(t, ctx, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
	deadlockPublicExec(t, ctx, b, "LOCK TABLE u IN ACCESS EXCLUSIVE MODE", 0)
	rb := deadlockPublicWorker(t, ctx, 1, func(ctx context.Context) (sql.Result, error) {
		return b.ExecContext(ctx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
	})
	rc := deadlockPublicWorker(t, ctx, 2, func(ctx context.Context) (sql.Result, error) {
		return c.ExecContext(ctx, "LOCK TABLE u IN ACCESS EXCLUSIVE MODE")
	})
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
}
