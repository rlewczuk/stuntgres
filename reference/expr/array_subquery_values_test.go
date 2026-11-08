package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// TestReferenceExprArraySubqueryValues pins the PostgreSQL 18.6 value, column
// name and wire metadata contract for ARRAY(SELECT ...) in projection and
// WHERE positions. See .cswd/tmp/GRIND-EXPR-ARRAY-SUBQUERY/oracle-matrix.md.
func TestReferenceExprArraySubqueryValues(t *testing.T) {
	arraySubqueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		cases := []struct {
			name  string
			query string
			names []string
			types []string
			rows  [][]any
		}{
			{"series", "SELECT ARRAY(SELECT g FROM generate_series(1,3) g)", []string{"array"}, []string{"_INT4"}, [][]any{{"{1,2,3}"}}},
			{"nulls_retained", "SELECT ARRAY(SELECT x FROM (VALUES (1),(NULL),(2)) v(x)) AS a", []string{"a"}, []string{"_INT4"}, [][]any{{"{1,NULL,2}"}}},
			{"empty", "SELECT ARRAY(SELECT g FROM generate_series(1,0) g)", []string{"array"}, []string{"_INT4"}, [][]any{{"{}"}}},
			{"unknown_literal", "SELECT ARRAY(SELECT 'a')", []string{"array"}, []string{"_TEXT"}, [][]any{{"{a}"}}},
			{"null_literal", "SELECT ARRAY(SELECT NULL)", []string{"array"}, []string{"_TEXT"}, [][]any{{"{NULL}"}}},
			{"order_by", "SELECT ARRAY(SELECT x FROM (VALUES (2),(1)) v(x) ORDER BY x DESC) AS a", []string{"a"}, []string{"_INT4"}, [][]any{{"{2,1}"}}},
			{"distinct", "SELECT ARRAY(SELECT DISTINCT x FROM (VALUES (2),(1),(2)) v(x) ORDER BY x) AS a", []string{"a"}, []string{"_INT4"}, [][]any{{"{1,2}"}}},
			{"aggregate_inside", "SELECT ARRAY(SELECT sum(g) FROM generate_series(1,3) g) AS a", []string{"a"}, []string{"_INT8"}, [][]any{{"{6}"}}},
			{"group_by_having", "SELECT ARRAY(SELECT g FROM generate_series(1,5) g GROUP BY g HAVING g%2=0 ORDER BY g DESC) AS a", []string{"a"}, []string{"_INT4"}, [][]any{{"{4,2}"}}},
			{"correlated", "SELECT g, ARRAY(SELECT x FROM generate_series(1,g) x) FROM generate_series(1,3) g ORDER BY g", []string{"g", "array"}, []string{"INT4", "_INT4"}, [][]any{{int64(1), "{1}"}, {int64(2), "{1,2}"}, {int64(3), "{1,2,3}"}}},
			{"where", "SELECT 1 AS one WHERE ARRAY(SELECT 1) = ARRAY[1]", []string{"one"}, []string{"INT4"}, [][]any{{int64(1)}}},
			{"cast_naming", "SELECT ARRAY(SELECT 1)::bigint[]", []string{"array"}, []string{"_INT8"}, [][]any{{"{1}"}}},
			{"array_typed_element", "SELECT ARRAY(SELECT ARRAY[1,2] UNION ALL SELECT ARRAY[3,4])", []string{"array"}, []string{"_INT4"}, [][]any{{"{{1,2},{3,4}}"}}},
			{"array_element_empty_set", "SELECT ARRAY(SELECT '{}'::int[] WHERE false)", []string{"array"}, []string{"_INT4"}, [][]any{{"{}"}}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				run := arraySubqueryRun(t, p, b, c)
				arraySubqueryBoth(run, fixture.ArrayReferenceSelect(tc.query, tc.names, tc.types, tc.types, tc.rows, b.PG))
			})
		}
		run := arraySubqueryRun(t, p, b, c)
		run(fixture.Exec("CREATE TABLE arrq_ins_direct(v int[])", 0))
		run(fixture.Exec("CREATE TABLE arrq_ins_prepared(v int[])", 0))
		for _, prepared := range []bool{false, true} {
			name, table := "direct", "arrq_ins_direct"
			if prepared {
				name, table = "prepared", "arrq_ins_prepared"
			}
			t.Run("insert_values/"+name, func(t *testing.T) {
				run := arraySubqueryRun(t, p, b, c)
				s := fixture.Exec("INSERT INTO "+table+"(v) VALUES (ARRAY(SELECT g FROM generate_series(1,2) g))", 1)
				if prepared {
					s = fixture.Prepared(s)
				}
				run(s)
				run(fixture.ArrayReferenceSelect("SELECT v FROM "+table, []string{"v"}, []string{"_INT4"}, []string{"_INT4"}, [][]any{{"{1,2}"}}, b.PG))
			})
		}
	})
}
