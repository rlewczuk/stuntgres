package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryRecursiveCTEWorktable(t *testing.T) {
	recursiveCTEReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		for _, tc := range []struct {
			q, typ string
			rows   [][]any
		}{
			{recursiveCTEFinite, "INT4", recursiveCTEInts(1, 2, 3)},
			{"WITH RECURSIVE r(n) AS (SELECT 1::bigint UNION ALL SELECT n+1 FROM r WHERE n<3) SELECT n FROM r", "INT8", recursiveCTEInts(1, 2, 3)},
			{"WITH RECURSIVE r(n) AS (SELECT n FROM (VALUES(1),(2)) s(n) UNION ALL SELECT n+1 FROM r WHERE n<3) SELECT n FROM r", "INT4", recursiveCTEInts(1, 2, 2, 3, 3)},
			{"WITH RECURSIVE r(n) AS (SELECT n FROM (VALUES(1),(2)) s(n) UNION SELECT n+1 FROM r WHERE n<3) SELECT n FROM r", "INT4", recursiveCTEInts(1, 2, 3)},
			{"WITH RECURSIVE r(n) AS (SELECT n FROM (VALUES(1),(1)) s(n) UNION SELECT n FROM r) SELECT n FROM r", "INT4", recursiveCTEInts(1)},
			{"WITH RECURSIVE r(n) AS (SELECT 1 UNION SELECT 10-n FROM r) SELECT n FROM r", "INT4", recursiveCTEInts(1, 9)},
			{"WITH RECURSIVE r(n) AS (SELECT 1 UNION DISTINCT SELECT 10-n FROM r) SELECT n FROM r", "INT4", recursiveCTEInts(1, 9)},
			{"WITH RECURSIVE r(n) AS (SELECT 1 WHERE FALSE UNION ALL SELECT 1/(n-n) FROM r) SELECT n FROM r", "INT4", nil},
		} {
			s := cteBag(tc.q, []string{"n"}, []string{tc.typ}, tc.rows)
			s.CheckMetadata = recursiveCTEMetadata(b, s.Columns, s.Types)
			recursiveCTERoutes(t, p, b, c, s)
		}
		for _, tc := range []struct {
			expr, typ, value         string
			length, precision, scale int64
			decimal                  bool
		}{
			{"'a'::varchar(3)", "VARCHAR", "a", 3, 0, 0, false},
			{"1.20::numeric(6,2)", "NUMERIC", "1.20", 0, 6, 2, true},
		} {
			for _, empty := range []string{"", " WHERE FALSE"} {
				q := "WITH RECURSIVE r(v) AS (SELECT " + tc.expr + empty + " UNION SELECT v FROM r) SELECT v FROM r"
				want := [][]any{{tc.value}}
				if empty != "" {
					want = nil
				}
				s := cteBag(q, []string{"v"}, []string{tc.typ}, want)
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					columns, err := rows.ColumnTypes()
					if err != nil || len(columns) != 1 {
						t.Fatal(columns, err)
					}
					column := columns[0]
					scan := reflect.TypeFor[string]()
					if b.PG && tc.decimal {
						scan = reflect.TypeFor[any]()
					}
					if column.Name() != "v" || column.DatabaseTypeName() != tc.typ || column.ScanType() != scan {
						t.Fatal("typed recursive descriptor", column)
					}
					if value, known := column.Nullable(); value || known {
						t.Fatal("recursive nullable", value, known)
					}
					if value, known := column.Length(); value != tc.length || known != !tc.decimal {
						t.Fatal("recursive length", value, known)
					}
					if precision, scale, known := column.DecimalSize(); precision != tc.precision || scale != tc.scale || known != tc.decimal {
						t.Fatal("recursive decimal", precision, scale, known)
					}
				}
				recursiveCTERoutes(t, p, b, c, s)
			}
		}
		run(fixture.Exec("CREATE TABLE recursive_edges(src INT,dst INT)", 0))
		run(fixture.Exec("INSERT INTO recursive_edges VALUES(1,2),(1,3),(2,4),(3,4),(4,1)", 5))
		walk := "WITH RECURSIVE r(n) AS (SELECT 1 UNION SELECT e.dst FROM r JOIN recursive_edges e ON e.src=r.n) SELECT n FROM r"
		recursiveCTERoutes(t, p, b, c, cteBag(walk, []string{"n"}, []string{"INT4"}, recursiveCTEInts(1, 2, 3, 4)))
		run(fixture.Exec("DELETE FROM recursive_edges WHERE src=4", 1))
		walkAll := "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT e.dst FROM r JOIN recursive_edges e ON e.src=r.n) SELECT n FROM r"
		recursiveCTERoutes(t, p, b, c, cteBag(walkAll, []string{"n"}, []string{"INT4"}, recursiveCTEInts(1, 2, 3, 4, 4)))
	})
}
