package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceStatisticalAggregateDiagnostics(t *testing.T) {
	statisticalScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, r *fixture.Recorder, pg bool) {
		fixture.RunStep(t, p.Ctx, conn, r, fixture.Exec("CREATE TABLE t(i INTEGER,b BOOLEAN)", 0))
		const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		type candidate struct{ q, code, message, position, hint string }
		cases := []candidate{}
		for _, name := range append(append([]string{}, statisticalNames...), regressionNames...) {
			valid := "i"
			if strings.HasPrefix(name, "regr_") {
				valid = "i,i"
			}
			for _, arg := range []struct{ sql, typ string }{{"TRUE", "boolean"}, {"'x'::text", "text"}, {"", ""}, {"*", ""}, {"1,2,3", "integer, integer, integer"}} {
				cases = append(cases, candidate{"SELECT " + name + "(" + arg.sql + ") FROM t WHERE FALSE", "42883", "function " + name + "(" + arg.typ + ") does not exist", "8", hint})
			}
			cases = append(cases,
				candidate{"SELECT /*é*/ " + name + "(TRUE) FROM t", "42883", "function " + name + "(boolean) does not exist", "14", hint},
				candidate{"SELECT pg_catalog." + name + "(TRUE) FROM t", "42883", "function pg_catalog." + name + "(boolean) does not exist", "8", hint},
				candidate{"SELECT public." + name + "(" + valid + ") FROM t", "42883", "function public." + name + "(" + strings.ReplaceAll(strings.ReplaceAll(valid, ",", ", "), "i", "integer") + ") does not exist", "8", hint},
				candidate{"SELECT missing_stats." + name + "(" + valid + ") FROM t", "3F000", `schema "missing_stats" does not exist`, "8", ""},
				candidate{"SELECT " + name + "(" + valid + ") FILTER (WHERE 1) FROM t", "42804", "argument of FILTER must be type boolean, not type integer", "", ""},
				candidate{"SELECT i FROM t WHERE " + name + "(" + valid + ")=0", "42803", "aggregate functions are not allowed in WHERE", "23", ""},
				candidate{"SELECT missing_stats." + name + "(" + strings.Repeat("1,", 100) + "1) FROM t", "54023", "cannot pass more than 100 arguments to a function", "8", ""},
			)
		}
		cases = append(cases,
			candidate{"SELECT var_pop(var_samp(i)) FROM t", "42803", "aggregate function calls cannot be nested", "16", ""},
			candidate{"SELECT regr_slope(i,var_pop(i)) FROM t", "42803", "aggregate function calls cannot be nested", "21", ""},
		)
		for _, c := range cases {
			t.Run(c.q, func(t *testing.T) {
				if strings.Contains(c.q, "FILTER") {
					c.position = strconv.Itoa(utf8.RuneCountInString(c.q[:strings.Index(c.q, "WHERE 1")+6]) + 1)
				}
				for _, prepared := range []bool{false, true} {
					for _, query := range []bool{false, true} {
						d := fixture.ControlsReferenceDiagnostic(c.code, c.message)
						d.Position, d.Hint = c.position, c.hint
						s := fixture.ErrorDiagnostic(c.q, d)
						s.Prepared, s.SelectRows, s.ErrorStage = prepared, query, "execute"
						if prepared {
							s.ErrorStage = "prepare"
						}
						fixture.RunStep(t, p.Ctx, conn, r, s)
					}
				}
			})
		}
	})
}
