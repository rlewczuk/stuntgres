package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestLockModesUtilityPrecedence(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER denied", "CREATE TABLE t(v INT)", "CREATE SEQUENCE seq", "CREATE VIEW v AS SELECT v FROM t", "CREATE SCHEMA hidden", "CREATE TABLE hidden.t(v INT)"} {
		roleExecute(t, e, op, q)
	}
	holder := sessionStart(t, e, nil)
	held := transactionBegin(t, holder)
	defer transactionEnd(t, holder, held, false)
	sessionSQL(t, holder, "LOCK t")
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION denied")
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
		_, err = e.Execute(op, statement(t, c.q), 0, nil)
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
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	// Catalog ID order is deliberately opposite definition order.
	for _, q := range []string{"CREATE TABLE later(v INT)", "CREATE TABLE first(v INT)", "CREATE VIEW v AS SELECT first.v FROM first JOIN later USING(v)"} {
		sessionSQL(t, a, q)
	}
	ra := transactionBegin(t, a)
	rb := transactionBegin(t, b)
	sessionSQL(t, a, "LOCK first")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := lockAsync(e, b, ctx, "LOCK v IN SHARE MODE", false)
	joined := false
	defer func() {
		cancel()
		if !joined {
			lockAwait(t, done, context.Canceled)
		}
		transactionEnd(t, b, rb, false)
		transactionEnd(t, a, ra, false)
	}()
	lockPending(t, e, 1)
	first := roleTableRef(t, e, "first")
	later := roleTableRef(t, e, "later")
	lockState := e.locks.Inspect()
	pending := len(lockState.Resources[lock.Resource{ObjectRef: first}].Pending)
	laterEntry := lockState.Resources[lock.Resource{ObjectRef: later}]
	if pending != 1 || laterEntry != nil {
		t.Fatal("view definition lock order was sorted/preflighted")
	}
	cancel()
	lockAwait(t, done, context.Canceled)
	joined = true
}
