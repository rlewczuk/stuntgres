package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

// These concrete base types admit typed NULL storage/parameters/casts but their
// released input routines refuse every non-NULL value. They intentionally have
// no invented seed or source in the non-NULL Cartesian cast inventory.
var castImplementedNullOnlyTypes = []struct {
	name, databaseType string
	oid                int64
}{
	{"pg_ndistinct", "PG_NDISTINCT", 3361},
	{"pg_dependencies", "PG_DEPENDENCIES", 3402},
	{"pg_mcv_list", "PG_MCV_LIST", 5017},
}

func TestReferenceCastImplementedInternalRoles(t *testing.T) {
	for _, typ := range castImplementedNullOnlyTypes {
		t.Run(typ.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					databaseType, scanType := typ.databaseType, reflect.TypeFor[string]()
					if b.PG {
						// lib/pq's fixed OID table does not name these real types.
						databaseType, scanType = "", reflect.TypeFor[any]()
					}
					metadata := func(t *testing.T, rows *sql.Rows) {
						columns, err := rows.ColumnTypes()
						if err != nil {
							t.Fatal(err)
						}
						if len(columns) != 1 || columns[0].DatabaseTypeName() != databaseType || columns[0].ScanType() != scanType {
							t.Fatal("internal cast result metadata", columns)
						}
					}
					for _, prepared := range []bool{false, true} {
						t.Run(fmt.Sprint(prepared), func(t *testing.T) {
							run := func(s fixture.Step) {
								if prepared {
									s = fixture.Prepared(s)
								}
								fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
							}
							run(fixture.Select("SELECT oid::bigint AS oid,typtype::text AS kind,typcategory::text AS category,typarray::bigint AS array_oid FROM pg_type WHERE typname=$1", []string{"oid", "kind", "category", "array_oid"}, []string{"INT8", "TEXT", "TEXT", "INT8"}, [][]any{{typ.oid, "b", "Z", int64(0)}}, typ.name))
							for _, query := range []string{"SELECT CAST(NULL AS " + typ.name + ") AS v", "SELECT CAST($1 AS " + typ.name + ") AS v"} {
								var args []any
								if query == "SELECT CAST($1 AS "+typ.name+") AS v" {
									args = []any{nil}
								}
								s := fixture.Select(query, []string{"v"}, []string{databaseType}, [][]any{{nil}}, args...)
								s.CheckMetadata = metadata
								run(s)
							}
							run(fixture.Exec("CREATE TABLE cast_internal_value(v "+typ.name+")", 0))
							run(fixture.Exec("INSERT INTO cast_internal_value VALUES(NULL)", 1))
							run(fixture.Exec("INSERT INTO cast_internal_value VALUES($1)", 1, nil))
							for _, input := range []string{"", "x"} {
								literal := fixture.ControlsReferenceDiagnostic("0A000", "cannot accept a value of type "+typ.name)
								literal.Position = "8"
								failure := fixture.ErrorDiagnostic("SELECT '"+input+"'::"+typ.name, literal)
								failure.SelectRows = true
								failure.ErrorStage = "execute"
								if prepared {
									failure.ErrorStage = "prepare"
								}
								run(failure)
								parameter := fixture.ControlsReferenceDiagnostic("0A000", "cannot accept a value of type "+typ.name)
								parameter.Context = "unnamed portal parameter $1 = '...'"
								if input == "" {
									parameter.Context = "unnamed portal parameter $1 = ''"
								}
								failure = fixture.ErrorDiagnostic("SELECT $1::"+typ.name, parameter, input)
								failure.SelectRows, failure.ErrorStage = true, "execute"
								run(failure)
							}
							stored := fixture.Select("SELECT v FROM cast_internal_value", []string{"v"}, []string{databaseType}, [][]any{{nil}, {nil}})
							stored.CheckMetadata = metadata
							run(stored)
							run(fixture.Exec("DROP TABLE cast_internal_value", 0))
						})
					}
				})
			}
		})
	}
}
