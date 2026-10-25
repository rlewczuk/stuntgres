package tablecmds_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

func TestAlterRenamePrivileges(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER rename_owner", "CREATE USER rename_schema_owner", "CREATE SCHEMA s AUTHORIZATION rename_schema_owner"} {
		enginetest.RoleExecute(t, e, op, q)
	}
	owner, _ := e.Roles().Lookup("rename_owner")
	schemaOwner, _ := e.Roles().Lookup("rename_schema_owner")
	view := e.CatalogRead()
	address := view.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "s"})
	ns := e.Catalog.Domain.Ref(view.Epoch(), address)
	acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
		{Grantor: schemaOwner.Ref, Grantee: authorization.ACLGrantee{Role: owner.Ref}, Privileges: authorization.UsagePrivilege | authorization.CreatePrivilege},
		{Grantor: schemaOwner.Ref, Grantee: authorization.ACLGrantee{Role: schemaOwner.Ref}, Privileges: authorization.UsagePrivilege | authorization.CreatePrivilege},
	}}
	if err := enginetest.SetObjectACL(e, context.Background(), ns, acl); err != nil {
		t.Fatal(err)
	}
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION rename_owner")
	enginetest.RoleExecute(t, e, op, "CREATE TABLE s.t(a INT)")
	enginetest.RoleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	acl.Entries[0].Privileges = authorization.UsagePrivilege
	if err := enginetest.SetObjectACL(e, context.Background(), ns, acl); err != nil {
		t.Fatal(err)
	}
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION rename_owner")
	enginetest.RoleExecute(t, e, op, "ALTER TABLE s.t RENAME a TO b")
	_, err := e.Execute(op, enginetest.Statement(t, "ALTER TABLE s.t RENAME TO renamed"), 0, nil)
	renameWantError(t, err, "42501", "permission denied for schema s")
	enginetest.RoleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION rename_schema_owner")
	_, err = e.Execute(op, enginetest.Statement(t, "ALTER TABLE s.t RENAME TO renamed"), 0, nil)
	renameWantError(t, err, "42501", "must be owner of table t")
	enginetest.RoleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	enginetest.RoleExecute(t, e, op, "ALTER TABLE s.t RENAME TO renamed")
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
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE USER rename_reader")
	enginetest.RoleExecute(t, e, op, "CREATE TABLE t(a INT,b TEXT,c BIGINT CONSTRAINT nn NOT NULL)")
	enginetest.RoleExecute(t, e, op, "INSERT INTO t VALUES(7,'drop',9)")
	enginetest.RoleExecute(t, e, op, "COMMENT ON COLUMN t.c IS 'keep'")
	enginetest.RoleExecute(t, e, op, "ALTER TABLE t DROP b")
	ref := enginetest.RoleTableRef(t, e, "t")
	column := ref
	column.Address.SubID = 3
	reader, _ := e.Roles().Lookup("rename_reader")
	acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: op.RoleState().CurrentUser, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege}}}
	if err := enginetest.SetObjectACL(e, context.Background(), column, acl); err != nil {
		t.Fatal(err)
	}
	old := e.Catalog.State.MustRelation(ref.Address)
	enginetest.RoleExecute(t, e, op, "ALTER TABLE t RENAME c TO renamed")
	enginetest.RoleExecute(t, e, op, "ALTER TABLE t RENAME TO newtable")
	next := e.Catalog.State.MustRelation(ref.Address)
	if !reflect.DeepEqual(next.Security.ColumnACL, old.Security.ColumnACL) || !reflect.DeepEqual(next.Constraints.NotNull, old.Constraints.NotNull) || len(next.Constraints.NotNull) != 1 || next.Constraints.NotNull[0].Name != "nn" || next.Constraints.NotNull[0].Attribute != 3 || next.Constraints.NotNull[0].OID == 0 || next.ColumnMeta.Comments[3] != "keep" {
		t.Fatal(next)
	}
	enginetest.RoleExecute(t, e, op, "COMMENT ON COLUMN newtable.renamed IS 'updated'")
	if e.Catalog.State.MustRelation(ref.Address).ColumnMeta.Comments[3] != "updated" || old.ColumnMeta.Comments[3] != "keep" {
		t.Fatal("comment identity")
	}
	_, err := e.Execute(op, enginetest.Statement(t, "DROP ROLE rename_reader"), 0, nil)
	var got *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: `role "rename_reader" cannot be dropped because some objects depend on it`, Detail: "privileges for column renamed of table newtable"}
	if !errors.As(err, &got) || *got != want {
		t.Fatal(err, got)
	}
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION rename_reader")
	enginetest.RoleExecute(t, e, op, "SELECT renamed FROM newtable")
	enginetest.RoleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	_, err = e.Execute(op, enginetest.Statement(t, "INSERT INTO newtable VALUES(8,NULL)"), 0, nil)
	want = sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "renamed" of relation "newtable" violates not-null constraint`, Detail: "Failing row contains (8, null).", Schema: "public", Table: "newtable", Column: "renamed"}
	if !errors.As(err, &got) || *got != want {
		t.Fatal(err, got, want)
	}
}
