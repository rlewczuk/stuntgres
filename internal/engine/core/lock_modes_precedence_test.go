package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

func TestLockModesUtilityPrecedence(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER denied", "CREATE TABLE t(v INT)", "CREATE SEQUENCE seq", "CREATE VIEW v AS SELECT v FROM t", "CREATE SCHEMA hidden", "CREATE TABLE hidden.t(v INT)"} {
		enginetest.RoleExecute(t, e, op, q)
	}
	holder := enginetest.StartSession(t, e, nil)
	held := enginetest.TransactionBegin(t, holder)
	defer enginetest.TransactionEnd(t, holder, held, false)
	enginetest.SessionSQL(t, holder, "LOCK t")
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION denied")
	for _, c := range []struct{ q, code, message, detail string }{
		{"LOCK t NOWAIT", "42501", "permission denied for table t", ""},
		{"LOCK v NOWAIT", "42501", "permission denied for view v", ""},
		{"LOCK seq NOWAIT", "42809", `cannot lock relation "seq"`, "This operation is not supported for sequences."},
		{"LOCK hidden.missing NOWAIT", "42501", "permission denied for schema hidden", ""},
	} {
		tx, err := e.Begin(op, session.TxOptions{})
		if err != nil {
			t.Fatal(err)
		}
		_, err = e.Execute(op, enginetest.Statement(t, c.q), 0, nil)
		var d *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: c.code, Message: c.message, Detail: c.detail}
		if !errors.As(err, &d) || *d != want {
			t.Fatalf("%s got %#v (%v), want %#v", c.q, d, err, want)
		}
		if _, err = e.Rollback(op, tx); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLockModesViewOrder(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	// Catalog ID order is deliberately opposite definition order.
	for _, q := range []string{"CREATE TABLE later(v INT)", "CREATE TABLE first(v INT)", "CREATE VIEW v AS SELECT first.v FROM first JOIN later USING(v)"} {
		enginetest.SessionSQL(t, a, q)
	}
	ra := enginetest.TransactionBegin(t, a)
	rb := enginetest.TransactionBegin(t, b)
	enginetest.SessionSQL(t, a, "LOCK first")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := enginetest.LockAsync(e, b, ctx, "LOCK v IN SHARE MODE", false)
	joined := false
	defer func() {
		cancel()
		if !joined {
			enginetest.LockAwait(t, done, context.Canceled)
		}
		enginetest.TransactionEnd(t, b, rb, false)
		enginetest.TransactionEnd(t, a, ra, false)
	}()
	enginetest.LockPending(t, e, 1)
	first := enginetest.RoleTableRef(t, e, "first")
	later := enginetest.RoleTableRef(t, e, "later")
	lockState := e.Locks.Inspect()
	pending := len(lockState.Resources[lock.Resource{ObjectRef: first}].Pending)
	laterEntry := lockState.Resources[lock.Resource{ObjectRef: later}]
	if pending != 1 || laterEntry != nil {
		t.Fatal("view definition lock order was sorted/preflighted")
	}
	cancel()
	enginetest.LockAwait(t, done, context.Canceled)
	joined = true
}
