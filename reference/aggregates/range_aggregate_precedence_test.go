package reference_test

import (
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRangeAggregateDiagnosticPrecedence(t *testing.T) {
	rangeAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE DOMAIN ra_integer AS INTEGER", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE r(v INT4RANGE)", 0))
		const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		for _, name := range []string{"range_agg", "range_intersect_agg"} {
			for _, tc := range []struct{ q, code, message, token, hint string }{
				{"SELECT " + name + "(1::ra_integer) FROM r", "42883", "function " + name + "(ra_integer) does not exist", name, hint},
				{"SELECT " + name + "(count(*),1) FROM r", "42883", "function " + name + "(bigint, integer) does not exist", name, hint},
				{"SELECT " + name + "(count(*)) FROM r", "42883", "function " + name + "(bigint) does not exist", name, hint},
				{"SELECT range_agg(" + name + "(1)) FROM r", "42883", "function " + name + "(integer) does not exist", name, hint},
				{"SELECT " + name + "(1) FILTER(WHERE missing) FROM r", "42703", `column "missing" does not exist`, "missing", ""},
				{"SELECT " + name + "(1) FILTER(WHERE 1) FROM r", "42804", "argument of FILTER must be type boolean, not type integer", "1)", ""},
				{"SELECT " + name + "(v) FILTER(WHERE range_agg(v) IS NULL) FROM r", "42803", "aggregate functions are not allowed in FILTER", "range_agg(v)", ""},
				{"CREATE TABLE ra_check(v INT4RANGE CHECK(" + name + "(v) IS NULL))", "42803", "aggregate functions are not allowed in check constraints", name, ""},
			} {
				t.Run(tc.q, func(t *testing.T) {
					position := strings.Index(tc.q, tc.token) + 1
					if tc.token == "1)" {
						position = strings.LastIndex(tc.q, "1)") + 1
					}
					if tc.message == "aggregate functions are not allowed in FILTER" {
						position = strings.LastIndex(tc.q, tc.token) + 1
					}
					if tc.q == "SELECT range_agg(range_agg(1)) FROM r" {
						position = strings.Index(tc.q, "(") + 2
					}
					d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					d.Hint, d.Position = tc.hint, strconv.Itoa(position)
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic(tc.q, d)
						s.Prepared, s.SelectRows, s.ErrorStage = prepared, strings.HasPrefix(tc.q, "SELECT"), "execute"
						if prepared && s.SelectRows {
							s.ErrorStage = "prepare"
						}
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
					}
				})
			}
		}
	})
}
