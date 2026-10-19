package engine

import (
	"database/sql/driver"
	"reflect"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestSchemaNestedConditionalTables(t *testing.T) {
	for _, skip := range []bool{false, true} {
		name := "fresh"
		if skip {
			name = "staged_skip"
		}
		t.Run(name, func(t *testing.T) {
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			roleExecute(t, e, op, "CREATE USER nested_owner")
			roleExecute(t, e, op, "SET search_path TO public")
			actor := op.RoleState()
			q := "CREATE SCHEMA nc AUTHORIZATION nested_owner CREATE TABLE IF NOT EXISTS t(id INT NOT NULL) CREATE TABLE clone(LIKE t)"
			if skip {
				q = "CREATE SCHEMA nc AUTHORIZATION nested_owner CREATE TABLE t(id INT NOT NULL) CREATE TABLE IF NOT EXISTS t(id unknown_type) CREATE TABLE IF NOT EXISTS t(LIKE unavailable) CREATE TABLE clone(LIKE t)"
			}
			roleExecute(t, e, op, q)
			if op.RoleState() != actor {
				t.Fatal("nested conditional changed actor")
			}
			if got := tuples(roleExecute(t, e, op, "SHOW search_path")); !reflect.DeepEqual(got, [][]driver.Value{{"public"}}) {
				t.Fatal("changed path", got)
			}
			var want []sqlstate.Error
			if skip {
				for range 2 {
					want = append(want, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "t" already exists, skipping`})
				}
			}
			if got := op.DetachNotices(); !reflect.DeepEqual(got, want) {
				t.Fatal("conditional notice records", got, want)
			}
			owner, _ := e.Roles().Lookup("nested_owner")
			for _, table := range []string{"t", "clone"} {
				ref := roleTableRef(t, e, "nc."+table)
				d := e.catalog.state.MustRelation(ref.Address)
				if d.Owner != owner.Ref || len(d.Columns) != 1 || d.Columns[0].Name != "id" || d.Columns[0].Usage.Type.DatabaseName() != "INT4" || d.Columns[0].Nullable || !d.Columns[0].NullabilityKnown || d.Security.ACL.Explicit || len(d.Security.ColumnACL) != 0 || d.Columns[0].OriginTable != uint32(ref.Address.ObjectID) || d.Columns[0].OriginAttribute != 1 {
					t.Fatal("conditional retained shape/owner/origin", table, d)
				}
				if got := roleExecute(t, e, op, "SELECT * FROM nc."+table); got.Count != 0 || len(got.Rows.Schema()) != 1 {
					t.Fatal("conditional table not empty", table, got)
				}
			}
			roleExecute(t, e, op, "INSERT INTO nc.t VALUES(7)")
			roleExecute(t, e, op, "INSERT INTO nc.clone VALUES(9)")
			for _, tc := range []struct {
				table string
				value int64
			}{{"t", 7}, {"clone", 9}} {
				if got := tuples(roleExecute(t, e, op, "SELECT * FROM nc."+tc.table)); !reflect.DeepEqual(got, [][]driver.Value{{tc.value}}) {
					t.Fatal(tc.table, got)
				}
			}
		})
	}
	t.Run("late_failure", func(t *testing.T) {
		e := New()
		op, done := roleOperation(t, e, "postgres")
		defer done()
		schemaFail(t, e, op, "CREATE SCHEMA ncl CREATE TABLE t(id INT) CREATE TABLE IF NOT EXISTS t(id unknown_type) CREATE TABLE bad(id INT,id TEXT)", "42701")
		if _, ok := e.CatalogRead().LookupName(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "ncl"}); ok {
			t.Fatal("late failure published schema")
		}
	})
	t.Run("whole_input_syntax", func(t *testing.T) {
		statements, err := parser.Parse("CREATE SCHEMA ncb CREATE TABLE IF NOT EXISTS t(id INT,)")
		if err == nil || statements != nil {
			t.Fatal("malformed conditional table accepted", statements, err)
		}
	})
}
