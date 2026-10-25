package schemacmds_test

import (
	"database/sql/driver"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
)

func TestSchemaLaterDropFailuresAtomic(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER batch_a", "CREATE USER batch_b", "CREATE SCHEMA first AUTHORIZATION batch_a CREATE TABLE t(id INT)", "CREATE SCHEMA later AUTHORIZATION batch_b CREATE TABLE t(id INT)", "CREATE SCHEMA empty_first AUTHORIZATION batch_a", "INSERT INTO first.t VALUES(7)", "INSERT INTO later.t VALUES(9)"} {
		enginetest.RoleExecute(t, e, op, q)
	}
	op.DetachNotices()
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION batch_a")
	schemaFail(t, e, op, "DROP SCHEMA first,later CASCADE", "42501")
	if n := op.DetachNotices(); len(n) != 0 {
		t.Fatal("failed batch emitted notices", n)
	}
	enginetest.RoleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	schemaFail(t, e, op, "DROP SCHEMA empty_first,later", "2BP01")
	if n := op.DetachNotices(); len(n) != 0 {
		t.Fatal("failed restrict emitted notices", n)
	}
	enginetest.RoleExecute(t, e, op, "CREATE TABLE empty_first.probe(id INT)")
	for _, tc := range []struct {
		name  string
		value int64
	}{{"first", 7}, {"later", 9}} {
		if got := enginetest.Tuples(enginetest.RoleExecute(t, e, op, "SELECT id FROM "+tc.name+".t")); !reflect.DeepEqual(got, [][]driver.Value{{tc.value}}) {
			t.Fatal(tc.name, got)
		}
	}
}
