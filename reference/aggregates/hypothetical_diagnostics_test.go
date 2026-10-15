package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceHypotheticalAggregateDiagnostics(t *testing.T) {
	hypotheticalScenario(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, b fixture.Backend) {
		hypotheticalSetup(t, p, c, b)
		for _, name := range []string{"rank", "dense_rank", "percent_rank", "cume_dist"} {
			for _, tc := range []struct {
				direct, order, signature string
				n, k                     int
			}{
				{"1", "1,2", "integer, integer, integer", 1, 2},
				{"", "1", "integer", 0, 1},
				{"1,2", "1", "integer, integer, integer", 2, 1},
			} {
				q := "SELECT " + name + "(" + tc.direct + ") WITHIN GROUP(ORDER BY " + tc.order + ")"
				t.Run(q, func(t *testing.T) { orderedSetBoth(t, p, c, b, hypotheticalArity(q, name, tc.signature, tc.n, tc.k)) })
			}
			for _, tc := range []struct{ q, code, message, token, hint, detail string }{
				{"SELECT " + name + "(1)", "42809", "WITHIN GROUP is required for ordered-set aggregate " + name, name, "", ""},
				{"SELECT " + name + "()", "42809", "window function " + name + " requires an OVER clause", name, "", ""},
				{"SELECT " + name + "(1) WITHIN GROUP(ORDER BY 1) OVER ()", "0A000", "OVER is not supported for ordered-set aggregate " + name, name, "", ""},
				{"SELECT " + name + "(1) WITHIN GROUP(ORDER BY 1,2) OVER ()", "0A000", "OVER is not supported for ordered-set aggregate " + name, name, "", ""},
				{"SELECT " + name + "(1) WITHIN GROUP(ORDER BY 'x'::text)", "42804", "WITHIN GROUP types text and integer cannot be matched", "1)", "", ""},
				{"SELECT " + name + "('x') WITHIN GROUP(ORDER BY 1)", "22P02", "invalid input syntax for type integer: \"x\"", "'x'", "", ""},
				{"SELECT " + name + "(v) WITHIN GROUP(ORDER BY v) FROM hs_input", "42803", "column \"hs_input.v\" must appear in the GROUP BY clause or be used in an aggregate function", "v)", "", "Direct arguments of an ordered-set aggregate must use only grouped columns."},
				{"SELECT " + name + "(1,v) WITHIN GROUP(ORDER BY v,v) FROM hs_input", "42803", "column \"hs_input.v\" must appear in the GROUP BY clause or be used in an aggregate function", "v)", "", "Direct arguments of an ordered-set aggregate must use only grouped columns."},
				{"SELECT " + name + "(sum(v)) WITHIN GROUP(ORDER BY v) FROM hs_input", "42803", "aggregate function calls cannot be nested", "sum", "", ""},
				{"SELECT " + name + "(1,sum(v)) WITHIN GROUP(ORDER BY v,v) FROM hs_input", "42803", "aggregate function calls cannot be nested", "sum", "", ""},
				{"SELECT " + name + "(DISTINCT 1) WITHIN GROUP(ORDER BY 1)", "42601", "cannot use DISTINCT with WITHIN GROUP", "WITHIN", "", ""},
				{"SELECT " + name + "(1 ORDER BY 1) WITHIN GROUP(ORDER BY 1)", "42601", "cannot use multiple ORDER BY clauses with WITHIN GROUP", "WITHIN", "", ""},
				{"SELECT " + name + "(VARIADIC ARRAY[1]) WITHIN GROUP(ORDER BY 1)", "42601", "cannot use VARIADIC with WITHIN GROUP", "WITHIN", "", ""},
				{"SELECT " + name + "(NULL::json) WITHIN GROUP(ORDER BY NULL::json)", "42883", "could not identify an ordering operator for type json", "NULL::json)", "Use an explicit ordering operator or modify the query.", ""},
				{"SELECT " + name + "(missing) WITHIN GROUP(ORDER BY v,v) FROM hs_input", "42703", "column \"missing\" does not exist", "missing", "", ""},
				{"SELECT " + name + "(1) WITHIN GROUP(ORDER BY v,v) FILTER(WHERE 2) FROM hs_input", "42804", "argument of FILTER must be type boolean, not type integer", "2)", "", ""},
				{"SELECT absent_hs_schema." + name + "(1) WITHIN GROUP(ORDER BY v) FROM hs_input", "3F000", "schema \"absent_hs_schema\" does not exist", "absent_hs_schema", "", ""},
				{"/*é😀*/ SELECT " + name + "(v) WITHIN GROUP(ORDER BY v) FROM hs_input", "42803", "column \"hs_input.v\" must appear in the GROUP BY clause or be used in an aggregate function", "v)", "", "Direct arguments of an ordered-set aggregate must use only grouped columns."},
				{"SELECT v FROM hs_input WHERE " + name + "(1) WITHIN GROUP(ORDER BY v)=1", "42803", "aggregate functions are not allowed in WHERE", name, "", ""},
				{"SELECT v FROM hs_input GROUP BY " + name + "(1) WITHIN GROUP(ORDER BY v)", "42803", "aggregate functions are not allowed in GROUP BY", name, "", ""},
			} {
				t.Run(tc.q, func(t *testing.T) {
					s := orderedSetDiagnostic(tc.q, tc.code, tc.message, tc.token, tc.hint)
					s.Diagnostic.Detail = tc.detail
					// JSON's diagnostic belongs to the ORDER operand, not the direct one.
					if tc.code == "42883" {
						s.Diagnostic.Position = fixture.Position(len("SELECT "+name+"(NULL::json) WITHIN GROUP(ORDER BY ") + 1)
					}
					orderedSetBoth(t, p, c, b, s)
				})
			}
		}
		for _, tc := range []struct{ direct, order, token string }{
			{"'a' COLLATE \"C\"", "'a' COLLATE \"POSIX\"", "COLLATE \"POSIX\""},
		} {
			q := "SELECT rank(" + tc.direct + ") WITHIN GROUP(ORDER BY " + tc.order + ")"
			orderedSetBoth(t, p, c, b, orderedSetDiagnostic(q, "42P21", "collation mismatch between explicit collations \"C\" and \"POSIX\"", tc.token, ""))
		}
	})
}
