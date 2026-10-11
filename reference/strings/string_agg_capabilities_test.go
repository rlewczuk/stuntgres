package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStringAggCapabilityBoundaries(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE capabilities(id INT,v TEXT,d TEXT)", 0))
			run(fixture.Exec("INSERT INTO capabilities VALUES(1,'a','/'),(2,'a','/'),(3,'a','!'),(4,NULL,'?')", 4))
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
							s := fixture.Select(tc.q, []string{tc.label}, []string{tc.typ}, tc.rows)
							if !backend.PG && tc.name == "over" {
								s = fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "0A000", Message: "explicit window frames are unsupported", Position: fixture.StringAggPosition(tc.q, "ROWS", false)})
								s.SelectRows = true
							}
							s.Prepared = prepared
							fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
						})
					}
				})
			}
			if !backend.PG {
				q := "INSERT INTO capabilities VALUES(5,'leaked','!'); SELECT string_agg(DISTINCT v,d ORDER BY id) FROM capabilities"
				s := fixture.Error(q, "0A000")
				run(s)
				run(fixture.Select("SELECT id FROM capabilities ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}}))
			}
		})
	}
}
