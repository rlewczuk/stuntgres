package reference_test

import "testing"

func TestReferenceArrayAggBoundaries(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			arrayAggSetup(run)
			for _, tc := range []struct{ q, want, action, token string }{
				{"SELECT array_agg(DISTINCT v ORDER BY v) FROM aa_array", "{{1,NULL},{3,4}}", "ordering", "v)"},
				{"SELECT array_agg(v ORDER BY v) FROM aa_array", "{{1,NULL},{3,4}}", "ordering", "v)"},
				{"SELECT array_agg(DISTINCT v) FROM aa_array WHERE id=1", "{{1,NULL}}", "DISTINCT", "v)"},
			} {
				s := arrayAggSelect(tc.q, "_INT4", [][]any{{tc.want}}, b.pg)
				if !b.pg {
					s = referenceImplicitError(tc.q, referenceDiagnostic{Code: "0A000", Message: "aggregate " + tc.action + " is unsupported for type integer[]", Position: stringAggPosition(tc.q, tc.token, false)})
					s.selectRows = true
				}
				arrayAggBoth(run, s)
			}
			q := "SELECT array_agg(v) OVER () FROM aa_scalar WHERE id=1"
			s := arrayAggSelect(q, "_INT4", [][]any{{"{3}"}}, b.pg)
			if !b.pg {
				s = referenceImplicitError(q, referenceDiagnostic{Code: "0A000", Message: "window function provider is unsupported", Position: "8"})
				s.selectRows = true
			}
			arrayAggBoth(run, s)
			q = "SELECT array_agg(v) FILTER(WHERE v IS NOT NULL) OVER () FROM aa_scalar WHERE id=1"
			s = arrayAggSelect(q, "_INT4", [][]any{{"{3}"}}, b.pg)
			if !b.pg {
				s = referenceImplicitError(q, referenceDiagnostic{Code: "0A000", Message: "window aggregate FILTER is unsupported", Position: stringAggPosition(q, "FILTER", false)})
				s.selectRows = true
			}
			arrayAggBoth(run, s)
			for _, tc := range []struct{ q, code, msg, hint string }{
				{"SELECT array_agg() OVER ()", "42883", "function array_agg() does not exist", "No function matches the given name and argument types. You might need to add explicit type casts."},
				{"SELECT array_agg(NULL) OVER ()", "42725", "function array_agg(unknown) is not unique", "Could not choose a best candidate function. You might need to add explicit type casts."},
				{"SELECT array_agg(DISTINCT v) OVER () FROM aa_scalar", "0A000", "DISTINCT is not implemented for window functions", ""},
				{"SELECT array_agg(v ORDER BY id) OVER () FROM aa_scalar", "0A000", "aggregate ORDER BY is not implemented for window functions", ""},
			} {
				s = referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.msg, Hint: tc.hint, Position: "8"})
				s.selectRows = true
				arrayAggBoth(run, s)
			}
		})
	}
}
