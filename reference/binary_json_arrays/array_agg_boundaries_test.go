package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceArrayAggBoundaries(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			arrayAggSetup(run)
			for _, tc := range []struct{ q, want, action, token string }{
				{"SELECT array_agg(DISTINCT v ORDER BY v) FROM aa_array", "{{1,NULL},{3,4}}", "ordering", "v)"},
				{"SELECT array_agg(v ORDER BY v) FROM aa_array", "{{1,NULL},{3,4}}", "ordering", "v)"},
				{"SELECT array_agg(DISTINCT v) FROM aa_array WHERE id=1", "{{1,NULL}}", "DISTINCT", "v)"},
			} {
				s := arrayAggSelect(tc.q, "_INT4", [][]any{{tc.want}}, b.PG)
				if !b.PG {
					s = fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "0A000", Message: "aggregate " + tc.action + " is unsupported for type integer[]", Position: fixture.StringAggPosition(tc.q, tc.token, false)})
					s.SelectRows = true
				}
				arrayAggBoth(run, s)
			}
			q := "SELECT array_agg(v) OVER () FROM aa_scalar WHERE id=1"
			s := arrayAggSelect(q, "_INT4", [][]any{{"{3}"}}, b.PG)
			if !b.PG {
				s = fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "window function provider is unsupported", Position: "8"})
				s.SelectRows = true
			}
			arrayAggBoth(run, s)
			q = "SELECT array_agg(v) FILTER(WHERE v IS NOT NULL) OVER () FROM aa_scalar WHERE id=1"
			s = arrayAggSelect(q, "_INT4", [][]any{{"{3}"}}, b.PG)
			if !b.PG {
				s = fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "window aggregate FILTER is unsupported", Position: fixture.StringAggPosition(q, "FILTER", false)})
				s.SelectRows = true
			}
			arrayAggBoth(run, s)
			for _, tc := range []struct{ q, code, msg, hint string }{
				{"SELECT array_agg() OVER ()", "42883", "function array_agg() does not exist", "No function matches the given name and argument types. You might need to add explicit type casts."},
				{"SELECT array_agg(NULL) OVER ()", "42725", "function array_agg(unknown) is not unique", "Could not choose a best candidate function. You might need to add explicit type casts."},
				{"SELECT array_agg(DISTINCT v) OVER () FROM aa_scalar", "0A000", "DISTINCT is not implemented for window functions", ""},
				{"SELECT array_agg(v ORDER BY id) OVER () FROM aa_scalar", "0A000", "aggregate ORDER BY is not implemented for window functions", ""},
			} {
				s = fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.msg, Hint: tc.hint, Position: "8"})
				s.SelectRows = true
				arrayAggBoth(run, s)
			}
		})
	}
}
