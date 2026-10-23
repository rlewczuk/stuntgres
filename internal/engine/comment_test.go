package engine

import (
	"context"
	"errors"
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"testing"
)

func commentWant(t *testing.T, e *Engine, read catalog.Read, ref ids.ObjectRef, want string) {
	t.Helper()
	got, present, err := e.Comment(read, ref)
	if err != nil || got != want || present != (want != "") {
		t.Fatalf("comment: %q %v %v want %q", got, present, err, want)
	}
}
func commentFailure(t *testing.T, e *Engine, op *session.Operation, q, code, message, detail string) {
	t.Helper()
	before := e.CatalogRead()
	rolesPublication := e.roles.published
	_, err := e.Execute(op, statement(t, q), 0, nil)
	var d *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Detail: detail}
	if !errors.As(err, &d) || *d != want {
		t.Fatalf("%s: full diagnostic %#v want %#v", q, d, want)
	}
	if before.State() != e.CatalogRead().State() || rolesPublication != e.roles.published {
		t.Fatal("failed COMMENT published metadata")
	}
}
func TestCommentMetadataLifecycle(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER owner", "CREATE SCHEMA s", "CREATE TABLE s.t(a INT,b TEXT)", "INSERT INTO s.t VALUES(7,'sentinel')"} {
		roleExecute(t, e, op, q)
	}
	table := roleTableRef(t, e, "s.t")
	a, b := table, table
	a.Address.SubID = 1
	b.Address.SubID = 2
	ns := schemaRef(t, e, "s")
	old := e.CatalogRead()
	oldRoles := e.Roles()
	for _, q := range []string{"COMMENT ON TABLE s.t IS 'table'", "COMMENT ON COLUMN s.t.a IS 'first'", "COMMENT ON COLUMN s.t.b IS 'second'", "COMMENT ON SCHEMA s IS 'schema'", "COMMENT ON ROLE owner IS 'role'"} {
		roleExecute(t, e, op, q)
	}
	for _, tc := range []struct {
		ref  ids.ObjectRef
		text string
	}{{table, "table"}, {a, "first"}, {b, "second"}, {ns, "schema"}} {
		commentWant(t, e, e.CatalogRead(), tc.ref, tc.text)
		commentWant(t, e, old, tc.ref, "")
	}
	if d, _ := oldRoles.Lookup("owner"); d.Comment != "" {
		t.Fatal("mutated retained role")
	}
	retainedRoles := e.Roles()
	for _, tc := range []struct{ sql, want string }{{"'replacement'", "replacement"}, {"NULL", ""}, {"''", ""}, {"' '", " "}, {"'role'", "role"}} {
		roleExecute(t, e, op, "COMMENT ON ROLE owner IS "+tc.sql)
		if d, _ := e.Roles().Lookup("owner"); d.Comment != tc.want {
			t.Fatal("role metadata roundtrip", d, tc.want)
		}
	}
	if d, _ := retainedRoles.Lookup("owner"); d.Comment != "role" {
		t.Fatal("mutated retained role value", d)
	}
	retained := e.CatalogRead()
	roleExecute(t, e, op, "COMMENT ON COLUMN s.t.a IS 'replacement'")
	commentWant(t, e, retained, a, "first")
	roleExecute(t, e, op, "COMMENT ON COLUMN s.t.a IS NULL")
	roleExecute(t, e, op, "COMMENT ON COLUMN s.t.b IS ''")
	commentWant(t, e, e.CatalogRead(), a, "")
	commentWant(t, e, e.CatalogRead(), b, "")
	roleExecute(t, e, op, "COMMENT ON COLUMN s.t.b IS ' '")
	commentWant(t, e, e.CatalogRead(), b, " ")
	for _, bad := range []ids.ObjectRef{{}, New().catalog.Domain.Ref(table.Epoch, table.Address), e.catalog.Domain.Ref(table.Epoch, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: table.Address.ObjectID, SubID: 99}), e.catalog.Domain.Ref(ns.Epoch, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ns.Address.ObjectID, SubID: 1})} {
		if _, _, err := e.Comment(e.CatalogRead(), bad); err == nil {
			t.Fatal("accepted invalid comment reference", bad)
		}
	}
	roleExecute(t, e, op, "CREATE TABLE s.copy(LIKE s.t)")
	commentWant(t, e, e.CatalogRead(), roleTableRef(t, e, "s.copy"), "")
	roleExecute(t, e, op, "ALTER SCHEMA s RENAME TO renamed")
	roleExecute(t, e, op, "ALTER SCHEMA renamed OWNER TO owner")
	commentWant(t, e, e.CatalogRead(), ns, "schema")
	commentWant(t, e, e.CatalogRead(), table, "table")
	roleExecute(t, e, op, "DROP SCHEMA renamed CASCADE")
	roleExecute(t, e, op, "CREATE SCHEMA renamed CREATE TABLE t(a INT,b TEXT)")
	commentWant(t, e, e.CatalogRead(), roleTableRef(t, e, "renamed.t"), "")
	roleExecute(t, e, op, "DROP ROLE owner")
	roleExecute(t, e, op, "CREATE USER owner")
	if d, _ := e.Roles().Lookup("owner"); d.Comment != "" {
		t.Fatal("role name reuse leaked text")
	}
	roleExecute(t, e, op, "COMMENT ON ROLE owner IS 'retained'")
	roleExecute(t, e, op, "COMMENT ON SCHEMA public IS 'bootstrap'")
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d, _ := e.Roles().Lookup("owner"); d.Comment != "retained" {
		t.Fatal(d)
	}
	commentWant(t, e, e.CatalogRead(), schemaRef(t, e, "public"), "standard public schema")
	if _, _, err := e.Comment(old, table); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("old epoch survived reset", err)
	}
}
func TestCommentAuthorizationAndLookup(t *testing.T) {
	e := New()
	admin, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER owner", "CREATE USER other", "CREATE SCHEMA s AUTHORIZATION owner", "SET SESSION AUTHORIZATION owner", "CREATE TABLE s.t(a INT)", "COMMENT ON TABLE s.t IS 'old'", "RESET SESSION AUTHORIZATION"} {
		roleExecute(t, e, admin, q)
	}
	other, closeOther := roleOperation(t, e, "other")
	defer closeOther()
	table := roleTableRef(t, e, "s.t")
	ns := schemaRef(t, e, "s")
	otherRole, _ := e.Roles().Lookup("other")
	ownerRole, _ := e.Roles().Lookup("owner")
	if err := e.SetObjectACL(context.Background(), ns, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantee: authorization.ACLGrantee{Role: otherRole.Ref}, Grantor: ownerRole.Ref, Privileges: authorization.UsagePrivilege | authorization.CreatePrivilege}}}); err != nil {
		t.Fatal(err)
	}
	if err := e.SetObjectACL(context.Background(), table, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantee: authorization.ACLGrantee{Role: otherRole.Ref}, Grantor: ownerRole.Ref, Privileges: authorization.SelectPrivilege | authorization.InsertPrivilege}}}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ q, code, message, detail string }{
		{"COMMENT ON TABLE s.t IS 'bad'", "42501", "must be owner of table t", ""},
		{"COMMENT ON COLUMN s.t.a IS 'bad'", "42501", "must be owner of relation t", ""},
		{"COMMENT ON SCHEMA s IS 'bad'", "42501", "must be owner of schema s", ""},
		{"COMMENT ON COLUMN s.t.missing IS 'bad'", "42703", `column "missing" of relation "s.t" does not exist`, ""},
		{"COMMENT ON TABLE s.missing IS 'bad'", "42P01", `relation "s.missing" does not exist`, ""},
		{"COMMENT ON SCHEMA missing IS NULL", "3F000", `schema "missing" does not exist`, ""},
		{"COMMENT ON COLUMN a IS NULL", "42601", "column name must be qualified", ""},
		{"COMMENT ON ROLE other IS 'bad'", "42501", "permission denied", "The current user must have the CREATEROLE attribute."},
		{"COMMENT ON ROLE postgres IS 'bad'", "42501", "permission denied", "The current user must have the SUPERUSER attribute."},
		{"COMMENT ON ROLE public IS NULL", "42704", `role "public" does not exist`, ""},
	} {
		commentFailure(t, e, other, tc.q, tc.code, tc.message, tc.detail)
	}
	commentWant(t, e, e.CatalogRead(), table, "old")
	held := statement(t, "COMMENT ON TABLE s.t IS 'prepared'")
	if _, err := e.Describe(admin, held); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, admin, "SET SESSION AUTHORIZATION other")
	before := e.CatalogRead()
	_, err := e.Execute(admin, held, 0, nil)
	state(t, err, "42501")
	if before.State() != e.CatalogRead().State() {
		t.Fatal("prepared authority cached")
	}
	roleExecute(t, e, admin, "RESET SESSION AUTHORIZATION")
	roleExecute(t, e, admin, "DROP TABLE s.t")
	roleExecute(t, e, admin, "CREATE TABLE s.t(a INT)")
	if _, err := e.Execute(admin, held, 0, nil); err != nil {
		t.Fatal(err)
	}
	commentWant(t, e, e.CatalogRead(), roleTableRef(t, e, "s.t"), "prepared")
	if err := e.SetObjectACL(context.Background(), ns, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	commentFailure(t, e, other, "COMMENT ON COLUMN s.t.missing IS NULL", "42501", "permission denied for schema s", "")
	roleExecute(t, e, other, "SET search_path TO s")
	commentFailure(t, e, other, "COMMENT ON TABLE t IS NULL", "42P01", `relation "t" does not exist`, "")
}

// Comment reads immutable metadata from r, a view e owns, without applying an
// ACL to its text.
func (e *Engine) Comment(r catalog.Read, ref ids.ObjectRef) (string, bool, error) {
	if r.Owner() == nil {
		return "", false, fmt.Errorf("stuntgres: foreign catalog view")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.catalog.validateObjectRefLocked(r, ref); err != nil {
		return "", false, err
	}
	a := ref.Address
	sub := a.SubID
	a.SubID = 0
	switch r.Get(a).(type) {
	case *catalog.Namespace, *catalog.Type, *catalog.Relation:
	default:
		return "", false, fmt.Errorf("stuntgres: object class has no comment metadata")
	}
	text := pgcatalog.DescriptorComment(r.Get(a), sub)
	return text, text != "", nil
}
