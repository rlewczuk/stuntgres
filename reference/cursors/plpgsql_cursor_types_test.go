package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLCursorTypes(t *testing.T) {
	sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		plCursorValue(t, p, b, c, "SELECT 'named_portal'::refcursor AS v", "REFCURSOR", "named_portal")
		plCursorValue(t, p, b, c, "SELECT NULL::refcursor AS v", "REFCURSOR", nil)
		plCursorValue(t, p, b, c, "SELECT $1::refcursor AS v", "REFCURSOR", "parameter_portal", "parameter_portal")
		plCursorValue(t, p, b, c, "SELECT $1::refcursor AS v", "REFCURSOR", nil, nil)
		plCursorValue(t, p, b, c, "SELECT ('named_portal'::refcursor)::text AS v", "TEXT", "named_portal")
		plCursorFunction(t, p, b, c, "pc_identity", "p refcursor", "refcursor", "DECLARE q refcursor; BEGIN q:=p; RETURN q; END")
		plCursorValue(t, p, b, c, "SELECT pc_identity($1::refcursor) AS v", "REFCURSOR", "copy_portal", "copy_portal")
		plCursorValue(t, p, b, c, "SELECT pc_identity(NULL::refcursor) AS v", "REFCURSOR", nil)
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT 'named_portal'::refcursor AS v", []string{"v"}, []string{"REFCURSOR"}, [][]any{{"named_portal"}})
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				types, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				scan := reflect.TypeFor[string]()
				if b.PG {
					scan = reflect.TypeFor[any]()
				}
				if len(types) != 1 || types[0].ScanType() != scan {
					t.Fatalf("refcursor ScanType: %v, want %v", types, scan)
				}
			}
			if prepared {
				s = fixture.Prepared(s)
			}
			sqlCursorStep(t, p, b, c, s)
		}
		s := fixture.Select("SELECT oid::integer AS oid,typcategory::text AS category,typcollation::integer AS coll,typarray::integer AS arr,typlen::integer AS len FROM pg_type WHERE typname='refcursor'", []string{"oid", "category", "coll", "arr", "len"}, []string{"INT4", "TEXT", "INT4", "INT4", "INT4"}, [][]any{{int64(1790), "U", int64(0), int64(2201), int64(-1)}})
		sqlCursorStep(t, p, b, c, s)
		sqlCursorStep(t, p, b, c, fixture.Prepared(s))
	})
}
