package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestLockModesViews(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER view_owner", "CREATE USER view_reader", "CREATE SCHEMA s AUTHORIZATION view_owner", "CREATE TABLE t(v INT)"} {
		roleExecute(t, e, op, q)
	}
	owner, _ := e.Roles().Lookup("view_owner")
	reader, _ := e.Roles().Lookup("view_reader")
	grantor := e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	entry := func(p authorization.PrincipalRef, r authorization.Privileges) authorization.ACLEntry {
		return authorization.ACLEntry{Grantor: grantor, Grantee: authorization.ACLGrantee{Role: p}, Privileges: r}
	}
	grant := func(ref ids.ObjectRef, entries ...authorization.ACLEntry) {
		t.Helper()
		if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: entries}); err != nil {
			t.Fatal(err)
		}
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION view_owner")
	for _, q := range []string{"CREATE VIEW s.owned AS SELECT v FROM public.t", "CREATE VIEW s.invoker WITH(security_invoker=true) AS SELECT v FROM public.t", "CREATE VIEW s.nested AS SELECT v FROM s.invoker"} {
		roleExecute(t, e, op, q)
	}
	roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	grant(schemaRef(t, e, "s"), entry(owner.Ref, authorization.UsagePrivilege|authorization.CreatePrivilege), entry(reader.Ref, authorization.UsagePrivilege))
	table := roleTableRef(t, e, "t")
	grant(table, entry(owner.Ref, authorization.UpdatePrivilege))
	for _, name := range []string{"s.owned", "s.invoker", "s.nested"} {
		grant(roleTableRef(t, e, name), entry(owner.Ref, authorization.UpdatePrivilege), entry(reader.Ref, authorization.UpdatePrivilege))
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION view_reader")
	for _, c := range []struct{ q, message string }{
		{"LOCK ONLY(s.owned) IN SHARE MODE NOWAIT", ""},
		{"LOCK s.invoker IN SHARE MODE NOWAIT", "permission denied for table t"},
		{"LOCK s.nested IN SHARE MODE NOWAIT", "permission denied for table t"},
	} {
		tx, err := e.Begin(op, session.TxOptions{})
		if err != nil {
			t.Fatal(err)
		}
		_, err = e.Execute(op, statement(t, c.q), 0, nil)
		if c.message == "" {
			if err != nil {
				t.Fatal(err)
			}
		} else {
			lockModesEngineDenied(t, err, c.message)
		}
		if _, err = e.Rollback(op, tx); err != nil {
			t.Fatal(err)
		}
	}
	roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	grant(table)
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION view_reader")
	tx, err := e.Begin(op, session.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Execute(op, statement(t, "LOCK s.owned IN SHARE MODE"), 0, nil)
	lockModesEngineDenied(t, err, "permission denied for table t")
	if _, err = e.Rollback(op, tx); err != nil {
		t.Fatal(err)
	}
	// Column UPDATE is not a table-level alternative, even through owner view.
	roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	column := table
	column.Address.SubID = 1
	grant(column, entry(owner.Ref, authorization.UpdatePrivilege))
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION view_reader")
	tx, err = e.Begin(op, session.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Execute(op, statement(t, "LOCK s.owned IN SHARE MODE"), 0, nil)
	lockModesEngineDenied(t, err, "permission denied for table t")
	if _, err = e.Rollback(op, tx); err != nil {
		t.Fatal(err)
	}
	lockEmpty(t, e)
}
func lockModesEngineDenied(t *testing.T, err error, message string) {
	t.Helper()
	var d *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: message}
	if !errors.As(err, &d) || *d != want {
		t.Fatalf("got %#v (%v), want %#v", d, err, want)
	}
}
