package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLCursorDefaultScroll(t *testing.T) {
	cases := []struct {
		name, query string
		backward    bool
		want        int64
	}{
		{"constant", "SELECT 7 AS i", false, 0},
		{"table", "SELECT 7 AS i FROM pc_rows", true, 7},
		{"table_order", "SELECT i FROM pc_rows ORDER BY i", true, 10},
		{"values_one", "VALUES (7)", false, 0},
		{"values_many", "VALUES (7),(7)", true, 7},
		{"from_srf", "SELECT g.i FROM generate_series(7,7) AS g(i)", true, 7},
		{"target_srf", "SELECT generate_series(7,7) AS i", false, 0},
		{"derived_table", "SELECT 7 AS i FROM (SELECT i FROM pc_rows) AS q", true, 7},
		{"derived_constant", "SELECT q.i FROM (SELECT 7 AS i) AS q", false, 0},
		{"derived_values_one", "SELECT q.i FROM (VALUES (7)) AS q(i)", false, 0},
		{"derived_values_many", "SELECT q.i FROM (VALUES (7),(7)) AS q(i)", true, 7},
		{"view_table", "SELECT 7 AS i FROM pc_scroll_view", true, 7},
		{"view_order", "SELECT i FROM pc_scroll_view ORDER BY i", true, 10},
		{"distinct", "SELECT DISTINCT 7 AS i FROM pc_rows", true, 7},
		{"distinct_order", "SELECT DISTINCT i FROM pc_rows ORDER BY i", true, 10},
		{"join", "SELECT 7 AS i FROM pc_rows a JOIN pc_rows b ON a.i=b.i", false, 0},
		{"aggregate", "SELECT sum(i)::integer AS i FROM pc_rows", false, 0},
		{"group_aggregate_order", "SELECT i FROM pc_rows GROUP BY i ORDER BY i", true, 10},
		{"union_all_constants", "SELECT 7 AS i UNION ALL SELECT 7 AS i", false, 0},
		{"union_all_tables", "SELECT 7 AS i FROM pc_rows UNION ALL SELECT 7 AS i FROM pc_rows", true, 7},
		{"union_distinct", "SELECT 7 AS i FROM pc_rows UNION SELECT 7 AS i FROM pc_rows", false, 0},
		{"intersect", "SELECT 7 AS i FROM pc_rows INTERSECT SELECT 7 AS i FROM pc_rows", false, 0},
		{"row_lock_order", "SELECT i FROM pc_rows ORDER BY i FOR UPDATE", false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				plCursorRows(t, p, b, c)
				sqlCursorStep(t, p, b, c, fixture.Exec("CREATE VIEW pc_scroll_view AS SELECT i FROM pc_rows", 0))
				plCursorFunction(t, p, b, c, "pc_scroll", "", "integer", "DECLARE c refcursor; x integer; BEGIN OPEN c FOR "+tc.query+"; FETCH c INTO x; FETCH c INTO x; FETCH PRIOR FROM c INTO x; CLOSE c; RETURN x; END")
				if tc.backward {
					plCursorValue(t, p, b, c, "SELECT pc_scroll() AS v", "INT4", tc.want)
				} else {
					want := plCursorRuntime("55000", "cursor can only scan forward", "pc_scroll()", "FETCH")
					want.Hint = "Declare it with SCROLL option to enable backward scan."
					plCursorError(t, p, b, c, "SELECT pc_scroll() AS v", want, true, false)
				}
			})
		})
	}
	plCursorCases(t, []plCursorCase{
		{"explicit_scroll_ordered_join", "c SCROLL CURSOR FOR SELECT a.i FROM pc_rows a JOIN pc_rows b ON a.i=b.i ORDER BY a.i; x integer;", "OPEN c; FETCH c INTO x; FETCH c INTO x; FETCH PRIOR FROM c INTO x; CLOSE c;", "x", "integer", int64(10)},
	})
	t.Run("explicit_no_scroll_ordered_join", func(t *testing.T) {
		sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			plCursorRows(t, p, b, c)
			plCursorFunction(t, p, b, c, "pc_scroll", "", "integer", "DECLARE c refcursor; x integer; BEGIN OPEN c NO SCROLL FOR SELECT a.i FROM pc_rows a JOIN pc_rows b ON a.i=b.i ORDER BY a.i; FETCH c INTO x; FETCH c INTO x; FETCH PRIOR FROM c INTO x; CLOSE c; RETURN x; END")
			want := plCursorRuntime("55000", "cursor can only scan forward", "pc_scroll()", "FETCH")
			want.Hint = "Declare it with SCROLL option to enable backward scan."
			plCursorError(t, p, b, c, "SELECT pc_scroll() AS v", want, true, false)
		})
	})
}
