package engine

import (
	"bytes"
	"context"
	"database/sql/driver"
	"reflect"
	"strings"
	"testing"
)

func TestSchemaTableIntegration(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{
		"CREATE USER sti_owner",
		"CREATE SCHEMA sti_first CREATE TABLE source(id INT NOT NULL)",
		"CREATE SCHEMA sti_second CREATE TABLE source(note TEXT)",
		"INSERT INTO sti_first.source VALUES(7)",
		"INSERT INTO sti_second.source VALUES('nine')",
		"SET search_path TO sti_second,sti_first",
		"CREATE TABLE sti_first.path_clone(LIKE source)",
		"CREATE TABLE sti_second.qualified_clone(lead BIGINT,LIKE sti_first.source,tail BOOL)",
		"INSERT INTO sti_first.path_clone VALUES('path')",
		"INSERT INTO sti_second.qualified_clone VALUES(9,7,true)",
	} {
		roleExecute(t, e, op, q)
	}
	assertRows := func(q string, want [][]driver.Value) {
		t.Helper()
		if got := tuples(roleExecute(t, e, op, q)); !reflect.DeepEqual(got, want) {
			t.Fatal(q, got, want)
		}
	}
	assertRows("SELECT * FROM sti_first.path_clone", [][]driver.Value{{"path"}})
	assertRows("SELECT * FROM sti_second.qualified_clone", [][]driver.Value{{int64(9), int64(7), true}})
	clone := roleTableRef(t, e, "sti_second.qualified_clone")
	d := e.state.descriptors[clone.Address].(*relationDescriptor)
	if len(d.columns) != 3 || d.columns[1].Name != "id" || d.columns[1].Nullable || !d.columns[1].NullabilityKnown || d.acl.Explicit || len(d.columnACL) != 0 {
		t.Fatal("expanded clone shape/authority", d)
	}
	for i, c := range d.columns {
		if c.OriginTable != uint32(clone.Address.ObjectID) || c.OriginAttribute != int16(i+1) {
			t.Fatal("copied source origin", c)
		}
	}
	beforeRole := op.RoleState()
	roleExecute(t, e, op, "CREATE SCHEMA sti_nested AUTHORIZATION sti_owner CREATE TABLE source(id INT NOT NULL) CREATE TABLE clone(lead BOOL,LIKE source) CREATE TABLE z() CREATE TABLE zl(LIKE z)")
	if op.RoleState() != beforeRole {
		t.Fatal("nested changed caller identity")
	}
	assertRows("SHOW search_path", [][]driver.Value{{"sti_second, sti_first"}})
	owner, _ := e.Roles().Lookup("sti_owner")
	for _, name := range []string{"source", "clone", "z", "zl"} {
		d := e.state.descriptors[roleTableRef(t, e, "sti_nested."+name).Address].(*relationDescriptor)
		if d.owner != owner.Ref || d.acl.Explicit || len(d.columnACL) != 0 {
			t.Fatal("nested table owner/ACL", name, d)
		}
	}
	if d := e.state.descriptors[roleTableRef(t, e, "sti_nested.zl").Address].(*relationDescriptor); len(d.columns) != 0 {
		t.Fatal("zero LIKE gained columns", d)
	}
	schemaFail(t, e, op, "CREATE SCHEMA sti_denied AUTHORIZATION sti_owner CREATE TABLE clone(LIKE sti_first.source)", "42501")
	for _, tc := range []struct{ q, code string }{
		{"CREATE SCHEMA sti_failed CREATE TABLE source(id INT) CREATE TABLE clone(LIKE source,id TEXT)", "42701"},
		{"CREATE SCHEMA sti_failed CREATE TABLE source(id INT) CREATE TABLE clone(LIKE source INCLUDING ALL)", "0A000"},
		{"CREATE SCHEMA sti_failed CREATE TABLE source(id INT) CREATE TABLE clone(id absent_type)", "42704"},
		{"DROP TABLE sti_first.source,sti_second.absent", "42P01"},
	} {
		schemaFail(t, e, op, tc.q, tc.code)
	}
	retained := roleExecute(t, e, op, "SELECT * FROM sti_first.source")
	old := e.CatalogRead()
	roleExecute(t, e, op, "DROP TABLE sti_first.source,sti_first.source RESTRICT")
	if _, ok := old.Descriptor(roleTableRef(t, e, "sti_first.path_clone").Address); !ok {
		t.Fatal("retained catalog lost survivor")
	}
	if got := tuples(retained); !reflect.DeepEqual(got, [][]driver.Value{{int64(7)}}) {
		t.Fatal("DROP changed retained rows", got)
	}
	assertRows("SELECT * FROM sti_second.qualified_clone", [][]driver.Value{{int64(9), int64(7), true}})
	// The schema owner can drop a differently owned table in its namespace.
	roleExecute(t, e, op, "CREATE TABLE sti_nested.other(id INT)")
	ordinary, ordinaryDone := roleOperation(t, e, "sti_owner")
	roleExecute(t, e, ordinary, "DROP TABLE sti_nested.other")
	ordinaryDone()
	roleExecute(t, e, op, "INSERT INTO sti_nested.clone VALUES(true,11)")
	var dump bytes.Buffer
	if err := e.Dump(context.Background(), &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "LIKE") || strings.Contains(dump.String(), `CREATE TABLE "sti_first"."source"`) || !strings.Contains(dump.String(), `CREATE TABLE "sti_nested"."zl" ();`) {
		t.Fatal("dump did not serialize independent expanded/zero shapes", dump.String())
	}
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := e.Restore(context.Background(), bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	assertRows("SELECT * FROM sti_second.qualified_clone", [][]driver.Value{{int64(9), int64(7), true}})
	assertRows("SELECT * FROM sti_nested.clone", [][]driver.Value{{true, int64(11)}})
	if d := e.state.descriptors[roleTableRef(t, e, "sti_nested.zl").Address].(*relationDescriptor); len(d.columns) != 0 || d.owner != owner.Ref {
		t.Fatal("restored zero shape/owner", d)
	}
}
