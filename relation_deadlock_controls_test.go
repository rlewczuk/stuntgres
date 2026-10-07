package stuntgres_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres"
	"testing"
	"time"
)

func deadlockPublicNegative(t *testing.T) {
	db := deadlockPublicDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	a := deadlockPublicConn(t, ctx, db)
	deadlockPublicExec(t, ctx, a, "CREATE TABLE t(v INT)", 0)
	for _, tc := range []struct{ q, code, message, pos string }{
		{"LOCK TABLE t IN ACCESS EXCLUSIVE MODE", "25P01", "LOCK TABLE can only be used in transaction blocks", ""},
		{"SELECT * FROM missing", "42P01", `relation "missing" does not exist`, "15"},
		{"SELECT * FROM t WHERE v = )", "42601", `syntax error at or near ")"`, "27"},
	} {
		_, err := a.ExecContext(ctx, tc.q)
		var d *stuntgres.Error
		if !errors.As(err, &d) {
			t.Fatal(err)
		}
		want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: tc.pos}
		if *d != want {
			t.Fatalf("negative diagnostic %#v want %#v", d, want)
		}
	}
	deadlockPublicExec(t, ctx, a, "CREATE ROLE deadlock_reader", 0)
	deadlockPublicExec(t, ctx, a, "SET SESSION AUTHORIZATION deadlock_reader", 0)
	deadlockPublicExec(t, ctx, a, "BEGIN", 0)
	_, err := a.ExecContext(ctx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
	var d *stuntgres.Error
	want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table t"}
	if !errors.As(err, &d) || *d != want {
		t.Fatalf("authorization diagnostic %#v (%v)", d, err)
	}
	deadlockPublicExec(t, ctx, a, "ROLLBACK", 0)
	deadlockPublicExec(t, ctx, a, "RESET SESSION AUTHORIZATION", 0)
	deadlockPublicExec(t, ctx, a, "BEGIN", 0)
	deadlockPublicExec(t, ctx, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", 0)
	deadlockPublicExec(t, ctx, a, "ROLLBACK", 0)
}

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

func TestRelationDeadlockPublicControls(t *testing.T) {
	deadlockPublicNegative(t)
	deadlockPublicAcyclic(t)
}
