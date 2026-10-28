package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBEachMetadata(t *testing.T) {
	jsonbEachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, f := range []string{"jsonb_each", "jsonb_each_text"} {
			for _, input := range []any{`{"a":1}`, `{}`, nil} {
				for _, tc := range []struct {
					q            string
					names, types []string
					row          []any
				}{
					{"SELECT " + f + "($1)", []string{f}, []string{"RECORD"}, []any{"(a,1)"}},
					{"SELECT " + f + "($1) AS r", []string{"r"}, []string{"RECORD"}, []any{"(a,1)"}},
					{"SELECT * FROM " + f + "($1)", []string{"key", "value"}, []string{"TEXT", jsonbEachType(f)}, []any{"a", "1"}},
					{"SELECT * FROM " + f + "($1) AS j", []string{"key", "value"}, []string{"TEXT", jsonbEachType(f)}, []any{"a", "1"}},
					{"SELECT * FROM " + f + "($1) AS j(k,v)", []string{"k", "v"}, []string{"TEXT", jsonbEachType(f)}, []any{"a", "1"}},
					{"SELECT * FROM " + f + "($1) AS j(k)", []string{"k", "value"}, []string{"TEXT", jsonbEachType(f)}, []any{"a", "1"}},
					{"SELECT * FROM " + f + "($1) WITH ORDINALITY AS j(k,v,n) ORDER BY n", []string{"k", "v", "n"}, []string{"TEXT", jsonbEachType(f), "INT8"}, []any{"a", "1", int64(1)}},
				} {
					t.Run(f+tc.q, func(t *testing.T) {
						var want [][]any
						if input == `{"a":1}` {
							want = [][]any{tc.row}
						}
						fixture.JSONEachBoth(t, p, b, c, jsonEachWithMetadata(fixture.Select(tc.q, tc.names, tc.types, want, input), b.PG))
					})
				}
			}
			value := "(a,null)"
			if f == "jsonb_each_text" {
				value = "(a,)"
			}
			fixture.JSONEachBoth(t, p, b, c, jsonEachWithMetadata(fixture.Select("SELECT "+f+"('{\"a\":null}'::jsonb)", []string{f}, []string{"RECORD"}, [][]any{{value}}), b.PG))
		}
	})
}
