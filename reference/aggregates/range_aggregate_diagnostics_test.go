package reference_test

import (
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceRangeAggregateDiagnostics(t *testing.T) {
	rangeAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE r(k INT,v INT4RANGE)", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO r VALUES(1,'empty'),(2,'[1,4)')", 2))
		const signatureHint = "No function matches the given name and argument types. You might need to add explicit type casts."
		const ambiguityHint = "Could not choose a best candidate function. You might need to add explicit type casts."
		for _, name := range []string{"range_agg", "range_intersect_agg"} {
			type candidate struct {
				q, code, message, position, hint string
				args                             []any
			}
			cases := []candidate{}
			for _, arg := range []struct{ sql, typ string }{{"1", "integer"}, {"'x'::text", "text"}, {"TRUE", "boolean"}, {"", ""}, {"*", ""}, {"1,2", "integer, integer"}} {
				cases = append(cases, candidate{"SELECT " + name + "(" + arg.sql + ") FROM r", "42883", "function " + name + "(" + arg.typ + ") does not exist", "8", signatureHint, nil})
			}
			for _, arg := range []string{"NULL", "'x'", "$1"} {
				var args []any
				if arg == "$1" {
					args = []any{nil}
				}
				cases = append(cases, candidate{"SELECT " + name + "(" + arg + ") FROM r", "42725", "function " + name + "(unknown) is not unique", "8", ambiguityHint, args})
			}
			cases = append(cases,
				candidate{"SELECT /*é*/ " + name + "(1) FROM r", "42883", "function " + name + "(integer) does not exist", "14", signatureHint, nil},
				candidate{"SELECT public." + name + "(v) FROM r", "42883", "function public." + name + "(int4range) does not exist", "8", signatureHint, nil},
				candidate{"SELECT ra_missing." + name + "(v) FROM r", "3F000", `schema "ra_missing" does not exist`, "8", "", nil},
				candidate{"SELECT " + name + "(" + strings.Repeat("1,", 99) + "1) FROM r", "42883", "function " + name + "(" + strings.Repeat("integer, ", 99) + "integer) does not exist", "8", signatureHint, nil},
				candidate{"SELECT ra_missing." + name + "(" + strings.Repeat("1,", 100) + "1) FROM r", "54023", "cannot pass more than 100 arguments to a function", "8", "", nil},
			)
			for _, q := range []string{"SELECT " + name + "(missing) FROM r", "SELECT ra_missing." + name + "(missing) FROM r", "SELECT ra_missing." + name + "(" + strings.Repeat("1,", 100) + "missing) FROM r", "SELECT " + name + "(count(*),missing) FROM r"} {
				pos := strconv.Itoa(utf8.RuneCountInString(q[:strings.LastIndex(q, "missing")]) + 1)
				cases = append(cases, candidate{q, "42703", `column "missing" does not exist`, pos, "", nil})
			}
			for _, q := range []string{"SELECT " + name + "(range_intersect_agg(v)) FROM r", "SELECT range_agg(" + name + "(v)) FROM r"} {
				pos := strconv.Itoa(strings.Index(q, "(") + 2)
				cases = append(cases, candidate{q, "42803", "aggregate function calls cannot be nested", pos, "", nil})
			}
			for _, clause := range []struct{ q, context string }{
				{"SELECT k FROM r WHERE " + name + "(v) IS NULL", "WHERE"},
				{"SELECT k FROM r GROUP BY " + name + "(v)", "GROUP BY"},
				{"SELECT r.k FROM r JOIN r AS z ON " + name + "(r.v) IS NULL", "JOIN conditions"},
				{"SELECT k FROM r LIMIT " + name + "(v)", "LIMIT"},
				{"SELECT k FROM r OFFSET " + name + "(v)", "OFFSET"},
			} {
				cases = append(cases, candidate{clause.q, "42803", "aggregate functions are not allowed in " + clause.context, strconv.Itoa(strings.Index(clause.q, name) + 1), "", nil})
			}
			for _, q := range []string{"SELECT " + name + "($1),$1::int4range FROM r GROUP BY 2", "SELECT " + name + "($1) FROM r WHERE $1::int4range IS NULL"} {
				cases = append(cases, candidate{q, "42725", "function " + name + "(unknown) is not unique", "8", ambiguityHint, []any{nil}})
			}
			for _, tc := range cases {
				t.Run(tc.q, func(t *testing.T) {
					for _, prepared := range []bool{false, true} {
						t.Run(strconv.FormatBool(prepared), func(t *testing.T) {
							d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
							d.Position, d.Hint = tc.position, tc.hint
							s := fixture.ErrorDiagnostic(tc.q, d, tc.args...)
							s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
							if prepared {
								s.ErrorStage = "prepare"
							}
							fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
						})
					}
				})
			}
			rangeAggregateRoutes(t, p, b, rangeAggregateStep(b.PG, "SELECT pg_catalog."+name+"(v) FROM r", []string{name}, []string{map[string]string{"range_agg": "INT4MULTIRANGE", "range_intersect_agg": "INT4RANGE"}[name]}, [][]any{{map[string]string{"range_agg": "{[1,4)}", "range_intersect_agg": "empty"}[name]}}))
		}
	})
}
