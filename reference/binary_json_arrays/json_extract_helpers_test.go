package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonExtractScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := c.Close(); err != nil {
					t.Error(err)
				}
			})
			body(t, p, b, c)
		})
	}
}

func jsonExtractOne(q, typ string, want any, args ...any) fixture.Step {
	return fixture.Select(q, []string{"v"}, []string{typ}, [][]any{{want}}, args...)
}

func jsonExtractBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, s fixture.Step) {
	t.Helper()
	jsonTypeBoth(t, p, b, c, jsonEachWithMetadata(s, b.PG))
}

func jsonExtractPathForms(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, doc, path string, parts []string, raw, text any) {
	t.Helper()
	for _, tc := range []struct {
		suffix, fn, typ string
		want            any
	}{
		{"#>", "json_extract_path", "JSON", raw}, {"#>>", "json_extract_path_text", "TEXT", text},
	} {
		queries := []string{"SELECT $1::json " + tc.suffix + " $2::text[] AS v", "SELECT " + tc.fn + "($1,VARIADIC $2::text[]) AS v"}
		for _, q := range queries {
			t.Run(q+path, func(t *testing.T) { jsonExtractBoth(t, p, b, c, jsonExtractOne(q, tc.typ, tc.want, doc, path)) })
		}
		if len(parts) > 0 {
			q := "SELECT " + tc.fn + "($1"
			args := []any{doc}
			for _, part := range parts {
				q += "," + jsonTypeQuote(part)
			}
			q += ") AS v"
			t.Run(q, func(t *testing.T) { jsonExtractBoth(t, p, b, c, jsonExtractOne(q, tc.typ, tc.want, args...)) })
		}
	}
}
