package core_test

import (
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
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
