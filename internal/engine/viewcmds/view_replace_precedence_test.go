package viewcmds_test

import (
	"database/sql/driver"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/sqlstate"
)

func TestViewReplaceSelectedTargetOwnershipPrecedence(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, kind := range []string{"table", "sequence"} {
			t.Run(fmt.Sprintf("%s/prepared=%v", kind, prepared), func(t *testing.T) {
				e := engine.New()
				op, done := enginetest.RoleOperation(t, e, "postgres")
				defer done()
				for _, q := range []string{"CREATE USER actor", "CREATE SCHEMA chosen AUTHORIZATION actor", "CREATE TABLE chosen.t(id INT)", "INSERT INTO chosen.t VALUES(1)"} {
					enginetest.RoleExecute(t, e, op, q)
				}
				if kind == "table" {
					enginetest.RoleExecute(t, e, op, "CREATE TABLE chosen.v(id INT)")
					enginetest.RoleExecute(t, e, op, "INSERT INTO chosen.v VALUES(7)")
				} else {
					enginetest.RoleExecute(t, e, op, "CREATE SEQUENCE chosen.v START WITH 7")
				}
				ref := enginetest.RoleTableRef(t, e, "chosen.v")
				original := e.CatalogRead().Get(ref.Address)
				stmt := enginetest.Statement(t, "CREATE OR REPLACE VIEW chosen.v AS SELECT id FROM chosen.t")
				enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION actor")
				before := e.CatalogRead().State()
				if prepared {
					if _, err := e.Describe(op, stmt); err != nil {
						t.Fatal("effect-free Prepare", err)
					}
					if e.CatalogRead().State() != before {
						t.Fatal("Prepare published")
					}
					enginetest.LockEmpty(t, e)
				}
				_, err := e.Execute(op, stmt, 0, nil)
				viewReplaceError(t, err, sqlstate.Error{Code: "42501", Message: "must be owner of " + kind + " v"})
				if e.CatalogRead().State() != before || e.CatalogRead().Get(ref.Address) != original {
					t.Fatal("nonowner failure changed target identity or catalog")
				}
				enginetest.LockEmpty(t, e)
				enginetest.RoleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
				// Ownership admits the target but does not make its kind a view.
				if prepared {
					if _, err := e.Describe(op, stmt); err != nil {
						t.Fatal(err)
					}
				}
				_, err = e.Execute(op, stmt, 0, nil)
				viewReplaceError(t, err, sqlstate.Error{Code: "42809", Message: `"v" is not a view`})
				if e.CatalogRead().State() != before || e.CatalogRead().Get(ref.Address) != original {
					t.Fatal("owner wrong-kind failure changed target")
				}
				enginetest.LockEmpty(t, e)
				query := "SELECT id FROM chosen.v"
				if kind == "sequence" {
					query = "SELECT nextval('chosen.v')"
				}
				if got := enginetest.Tuples(enginetest.Run(t, e, query)); !reflect.DeepEqual(got, [][]driver.Value{{int64(7)}}) {
					t.Fatal("target kind/data changed", got)
				}
				if got := enginetest.Tuples(enginetest.Run(t, e, "SELECT id FROM chosen.t")); !reflect.DeepEqual(got, [][]driver.Value{{int64(1)}}) {
					t.Fatal("source changed", got)
				}
			})
		}
	}
}
