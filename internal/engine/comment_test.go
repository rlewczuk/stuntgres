package engine

import (
	"context"
	"errors"
	"fmt"
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
	rolesPublication := e.RoleStore.Published
	_, err := e.Execute(op, statement(t, q), 0, nil)
	var d *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Detail: detail}
	if !errors.As(err, &d) || *d != want {
		t.Fatalf("%s: full diagnostic %#v want %#v", q, d, want)
	}
	if before.State() != e.CatalogRead().State() || rolesPublication != e.RoleStore.Published {
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
	roleExecute(t, e, op, "COMMENT ON ROLE owner IS 'retained'")
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
	for _, bad := range []ids.ObjectRef{{}, New().Catalog.Domain.Ref(table.Epoch, table.Address), e.Catalog.Domain.Ref(table.Epoch, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: table.Address.ObjectID, SubID: 99}), e.Catalog.Domain.Ref(ns.Epoch, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ns.Address.ObjectID, SubID: 1})} {
		if _, _, err := e.Comment(e.CatalogRead(), bad); err == nil {
			t.Fatal("accepted invalid comment reference", bad)
		}
	}
	roleExecute(t, e, op, "COMMENT ON SCHEMA public IS 'bootstrap'")
	if err := Reset(context.Background(), e); err != nil {
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

// Comment reads immutable metadata from r, a view e owns, without applying an
// ACL to its text.
func (e *Engine) Comment(r catalog.Read, ref ids.ObjectRef) (string, bool, error) {
	if r.Owner() == nil {
		return "", false, fmt.Errorf("stuntgres: foreign catalog view")
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if err := e.Catalog.ValidateObjectRefLocked(r, ref); err != nil {
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
