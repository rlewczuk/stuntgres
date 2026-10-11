package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONCasts(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		for _, tc := range []struct {
			q, typ string
			want   any
			args   []any
		}{
			{`SELECT json 'null' AS v`, "JSON", "null", nil},
			{`SELECT pg_catalog.json 'null' AS v`, "JSON", "null", nil},
			{`SELECT CAST(' {"a":1,"a":2} ' AS json) AS v`, "JSON", ` {"a":1,"a":2} `, nil},
			{`SELECT '"\u0000"'::json AS v`, "JSON", `"\u0000"`, nil},
			{`SELECT CAST(NULL AS json) AS v`, "JSON", nil, nil},
			{`SELECT CAST(CAST('null' AS json) AS json) AS v`, "JSON", "null", nil},
			{`SELECT CAST(CAST(' {"b":2,"a":1} ' AS json) AS text) AS v`, "TEXT", ` {"b":2,"a":1} `, nil},
			{`SELECT CAST(CAST('null' AS text) AS json) AS v`, "JSON", "null", nil},
			{`SELECT CAST($1 AS json) AS v`, "JSON", ` [1, 2] `, []any{` [1, 2] `}},
			{`SELECT $1::json AS v`, "JSON", "null", []any{"null"}},
		} {
			for _, prepared := range []bool{false, true} {
				t.Run(tc.q+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
					s := fixture.Select(tc.q, []string{"v"}, []string{tc.typ}, [][]any{{tc.want}}, tc.args...)
					if prepared {
						s = fixture.Prepared(s)
					}
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
				})
			}
		}
	}
}
