package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestAlterRenamePrivileges(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER rename_owner", "CREATE USER rename_schema_owner", "CREATE SCHEMA s AUTHORIZATION rename_schema_owner"} {
		roleExecute(t, e, op, q)
	}
	owner, _ := e.Roles().Lookup("rename_owner")
	schemaOwner, _ := e.Roles().Lookup("rename_schema_owner")
	view := e.CatalogRead()
	address := view.state.names[NameKey{ClassID: NamespaceClassID, Name: "s"}]
	ns := ObjectRef{owner: e.owner, Epoch: view.Epoch(), Address: address}
	acl := ACLValue{Explicit: true, Entries: []ACLEntry{
		{Grantor: schemaOwner.Ref, Grantee: ACLGrantee{Role: owner.Ref}, Privileges: UsagePrivilege | CreatePrivilege},
		{Grantor: schemaOwner.Ref, Grantee: ACLGrantee{Role: schemaOwner.Ref}, Privileges: UsagePrivilege | CreatePrivilege},
	}}
	if err := e.SetObjectACL(context.Background(), ns, acl); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION rename_owner")
	roleExecute(t, e, op, "CREATE TABLE s.t(a INT)")
	roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	acl.Entries[0].Privileges = UsagePrivilege
	if err := e.SetObjectACL(context.Background(), ns, acl); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION rename_owner")
	roleExecute(t, e, op, "ALTER TABLE s.t RENAME a TO b")
	_, err := e.Execute(op, statement(t, "ALTER TABLE s.t RENAME TO renamed"), 0, nil)
	renameWantError(t, err, "42501", "permission denied for schema s")
	roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION rename_schema_owner")
	_, err = e.Execute(op, statement(t, "ALTER TABLE s.t RENAME TO renamed"), 0, nil)
	renameWantError(t, err, "42501", "must be owner of table t")
	roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	roleExecute(t, e, op, "ALTER TABLE s.t RENAME TO renamed")
}

func renameWantError(t *testing.T, err error, code, message string) {
	t.Helper()
	var got *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
	if !errors.As(err, &got) || *got != want {
		t.Fatal(err, got, want)
	}
}

func TestAlterRenameDependencies(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE USER rename_reader")
	roleExecute(t, e, op, "CREATE TABLE t(a INT,b TEXT,c BIGINT CONSTRAINT nn NOT NULL)")
	roleExecute(t, e, op, "INSERT INTO t VALUES(7,'drop',9)")
	roleExecute(t, e, op, "COMMENT ON COLUMN t.c IS 'keep'")
	roleExecute(t, e, op, "ALTER TABLE t DROP b")
	ref := roleTableRef(t, e, "t")
	column := ref
	column.Address.SubID = 3
	reader, _ := e.Roles().Lookup("rename_reader")
	acl := ACLValue{Explicit: true, Entries: []ACLEntry{{Grantor: op.RoleState().CurrentUser, Grantee: ACLGrantee{Role: reader.Ref}, Privileges: SelectPrivilege}}}
	if err := e.SetObjectACL(context.Background(), column, acl); err != nil {
		t.Fatal(err)
	}
	old := e.state.descriptors[ref.Address].(*relationDescriptor)
	roleExecute(t, e, op, "ALTER TABLE t RENAME c TO renamed")
	roleExecute(t, e, op, "ALTER TABLE t RENAME TO newtable")
	next := e.state.descriptors[ref.Address].(*relationDescriptor)
	if !reflect.DeepEqual(next.columnACL, old.columnACL) || !reflect.DeepEqual(next.notNullConstraints, []notNullConstraint{{Name: "nn", Attribute: 3}}) || next.columnComments[3] != "keep" {
		t.Fatal(next)
	}
	roleExecute(t, e, op, "COMMENT ON COLUMN newtable.renamed IS 'updated'")
	if e.state.descriptors[ref.Address].(*relationDescriptor).columnComments[3] != "updated" || old.columnComments[3] != "keep" {
		t.Fatal("comment identity")
	}
	_, err := e.Execute(op, statement(t, "DROP ROLE rename_reader"), 0, nil)
	var got *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: `role "rename_reader" cannot be dropped because some objects depend on it`, Detail: "privileges for column renamed of table newtable"}
	if !errors.As(err, &got) || *got != want {
		t.Fatal(err, got)
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION rename_reader")
	roleExecute(t, e, op, "SELECT renamed FROM newtable")
	roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	_, err = e.Execute(op, statement(t, "INSERT INTO newtable VALUES(8,NULL)"), 0, nil)
	want = sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "renamed" of relation "newtable" violates not-null constraint`, Detail: "Failing row contains (8, null).", Schema: "public", Table: "newtable", Column: "renamed"}
	if !errors.As(err, &got) || *got != want {
		t.Fatal(err, got, want)
	}
}
