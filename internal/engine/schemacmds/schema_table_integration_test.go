package schemacmds_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
)

func TestSchemaTableIntegration(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
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
		enginetest.RoleExecute(t, e, op, q)
	}
	clone := enginetest.RoleTableRef(t, e, "sti_second.qualified_clone")
	d := e.Catalog.State.MustRelation(clone.Address)
	if len(d.Columns) != 3 || d.Columns[1].Name != "id" || d.Columns[1].Nullable || !d.Columns[1].NullabilityKnown || d.Security.ACL.Explicit || len(d.Security.ColumnACL) != 0 {
		t.Fatal("expanded clone shape/authority", d)
	}
	for i, c := range d.Columns {
		if c.OriginTable != uint32(clone.Address.ObjectID) || c.OriginAttribute != int16(i+1) {
			t.Fatal("copied source origin", c)
		}
	}
	beforeRole := op.RoleState()
	enginetest.RoleExecute(t, e, op, "CREATE SCHEMA sti_nested AUTHORIZATION sti_owner CREATE TABLE source(id INT NOT NULL) CREATE TABLE clone(lead BOOL,LIKE source) CREATE TABLE z() CREATE TABLE zl(LIKE z)")
	if op.RoleState() != beforeRole {
		t.Fatal("nested changed caller identity")
	}
	owner, _ := e.Roles().Lookup("sti_owner")
	for _, name := range []string{"source", "clone", "z", "zl"} {
		d := e.Catalog.State.MustRelation(enginetest.RoleTableRef(t, e, "sti_nested."+name).Address)
		if d.Owner != owner.Ref || d.Security.ACL.Explicit || len(d.Security.ColumnACL) != 0 {
			t.Fatal("nested table owner/ACL", name, d)
		}
	}
	if d := e.Catalog.State.MustRelation(enginetest.RoleTableRef(t, e, "sti_nested.zl").Address); len(d.Columns) != 0 {
		t.Fatal("zero LIKE gained columns", d)
	}
	retained := enginetest.RoleExecute(t, e, op, "SELECT * FROM sti_first.source")
	old := e.CatalogRead()
	enginetest.RoleExecute(t, e, op, "DROP TABLE sti_first.source,sti_first.source RESTRICT")
	if _, ok := old.Descriptor(enginetest.RoleTableRef(t, e, "sti_first.path_clone").Address); !ok {
		t.Fatal("retained catalog lost survivor")
	}
	if got := enginetest.Tuples(retained); !reflect.DeepEqual(got, [][]driver.Value{{int64(7)}}) {
		t.Fatal("DROP changed retained rows", got)
	}
	// The schema owner can drop a differently owned table in its namespace.
	enginetest.RoleExecute(t, e, op, "CREATE TABLE sti_nested.other(id INT)")
	ordinary, ordinaryDone := enginetest.RoleOperation(t, e, "sti_owner")
	enginetest.RoleExecute(t, e, ordinary, "DROP TABLE sti_nested.other")
	ordinaryDone()
	enginetest.RoleExecute(t, e, op, "INSERT INTO sti_nested.clone VALUES(true,11)")
	// Replay behavior lives in the shared reference scenario; here inspect
	// private restored ownership and zero-column descriptor invariants.
	var dump bytes.Buffer
	if err := engine.Dump(context.Background(), e, &dump); err != nil {
		t.Fatal(err)
	}
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if err := engine.Restore(context.Background(), e, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if d := e.Catalog.State.MustRelation(enginetest.RoleTableRef(t, e, "sti_nested.zl").Address); len(d.Columns) != 0 || d.Owner != owner.Ref {
		t.Fatal("restored zero shape/owner", d)
	}
}
