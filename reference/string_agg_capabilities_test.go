package reference_test

import "testing"

func TestReferenceStringAggCapabilityBoundaries(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE capabilities(id INT,v TEXT,d TEXT)", 0))
			run(referenceExec("INSERT INTO capabilities VALUES(1,'a','/'),(2,'a','/'),(3,'a','!'),(4,NULL,'?')", 4))
			for _, tc := range []struct {
				name, q, label, typ string
				rows                [][]any
			}{
				{"distinct-tuples", "SELECT string_agg(DISTINCT v,d ORDER BY d) FROM capabilities", "string_agg", "TEXT", [][]any{{"a/a"}}},
				{"all", "SELECT string_agg(ALL v,d ORDER BY id) FROM capabilities", "string_agg", "TEXT", [][]any{{"a/a!a"}}},
				{"filter-true", "SELECT string_agg(v,d ORDER BY id) FILTER (WHERE id=3) FROM capabilities", "string_agg", "TEXT", [][]any{{"a"}}},
				{"filter-false", "SELECT string_agg(v,d ORDER BY id) FILTER (WHERE FALSE) FROM capabilities", "string_agg", "TEXT", [][]any{{nil}}},
				{"filter-null", "SELECT string_agg(v,d ORDER BY id) FILTER (WHERE NULL) FROM capabilities", "string_agg", "TEXT", [][]any{{nil}}},
				{"over", "SELECT string_agg(v,d) OVER (ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) FROM capabilities ORDER BY id", "string_agg", "TEXT", [][]any{{"a/a!a"}, {"a/a!a"}, {"a/a!a"}, {"a/a!a"}}},
				{"sibling-order", "SELECT count(v ORDER BY id) FROM capabilities", "count", "INT8", [][]any{{int64(3)}}},
				{"no-from", "SELECT string_agg('x',',')", "string_agg", "TEXT", [][]any{{"x"}}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					for _, prepared := range []bool{false, true} {
						t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
							s := referenceSelect(tc.q, []string{tc.label}, []string{tc.typ}, tc.rows)
							if !backend.pg {
								message, position := "aggregate modifiers are unsupported", ""
								switch tc.name {
								case "distinct-tuples":
									message, position = "aggregate DISTINCT is unsupported", "8"
								case "all":
									message, position = "aggregate ALL is unsupported", "8"
								case "over":
									position = stringAggPosition(tc.q, "OVER", false)
								case "sibling-order":
									message, position = "aggregate ordering is unsupported", "8"
								case "no-from":
									message, position = "SELECT without FROM is unsupported", "1"
								default:
									position = stringAggPosition(tc.q, "FILTER", false)
								}
								s = referenceImplicitError(tc.q, referenceDiagnostic{Code: "0A000", Message: message, Position: position})
								s.selectRows = true
							}
							s.prepared = prepared
							referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
						})
					}
				})
			}
			if !backend.pg {
				q := "INSERT INTO capabilities VALUES(5,'leaked','!'); SELECT string_agg(DISTINCT v,d) FROM capabilities"
				s := referenceImplicitError(q, referenceDiagnostic{Code: "0A000", Message: "aggregate DISTINCT is unsupported", Position: stringAggPosition(q, "string_agg", false)})
				run(s)
				run(referenceSelect("SELECT id FROM capabilities ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}}))
			}
		})
	}
}
