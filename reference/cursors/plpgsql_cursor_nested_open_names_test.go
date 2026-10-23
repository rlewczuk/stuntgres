package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLCursorNestedOpenNames(t *testing.T) {
	for _, tc := range []struct{ name, declaration, open string }{
		{"bound_actual", "c CURSOR(p refcursor) FOR SELECT p;", "OPEN c(pc_nested_open());"},
		{"dynamic_using", "c refcursor;", "OPEN c FOR EXECUTE 'SELECT $1::refcursor' USING pc_nested_open();"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				plCursorFunction(t, p, b, c, "pc_nested_open", "", "refcursor", "DECLARE p refcursor; BEGIN OPEN p FOR SELECT 100; RETURN p; END")
				body := "DECLARE " + tc.declaration + " inner_ref refcursor; x integer; distinct_names boolean; BEGIN " + tc.open +
					" FETCH c INTO inner_ref; distinct_names:=c::text<>inner_ref::text; FETCH inner_ref INTO x; CLOSE inner_ref; CLOSE c; RETURN x::text||'/'||distinct_names::text; END"
				plCursorFunction(t, p, b, c, "pc_nested_outer", "", "text", body)
				// Identity is relational: no allocator ordinal or spelling is assumed.
				// Both the nested and containing unnamed portals must survive until
				// their own FETCH, and both explicit CLOSE commands must succeed.
				plCursorValue(t, p, b, c, "SELECT pc_nested_outer() AS v", "TEXT", "100/true")
				plCursorValue(t, p, b, c, "SELECT count(*) AS v FROM pg_cursors WHERE name<>''", "INT8", int64(0))
			})
		})
	}
}
