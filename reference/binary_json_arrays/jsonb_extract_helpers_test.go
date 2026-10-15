package reference_test

import (
	"database/sql"
	"encoding/json"
	"testing"

	"stuntgres/reference/fixture"
)

func jsonbExtractScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
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

func jsonbExtractPathForms(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, doc, path any, parts []string, raw, text any) {
	t.Helper()
	for _, tc := range []struct {
		op, fn, typ string
		want        any
	}{
		{"#>", "jsonb_extract_path", "JSONB", raw},
		{"#>>", "jsonb_extract_path_text", "TEXT", text},
	} {
		for _, q := range []string{"SELECT $1::jsonb " + tc.op + " $2::text[] AS v", "SELECT " + tc.fn + "($1,VARIADIC $2::text[]) AS v"} {
			t.Run(q, func(t *testing.T) { jsonExtractBoth(t, p, b, c, jsonExtractOne(q, tc.typ, tc.want, doc, path)) })
		}
		if len(parts) != 0 {
			q := "SELECT " + tc.fn + "($1"
			for _, part := range parts {
				q += "," + jsonTypeQuote(part)
			}
			q += ") AS v"
			t.Run(q, func(t *testing.T) { jsonExtractBoth(t, p, b, c, jsonExtractOne(q, tc.typ, tc.want, doc)) })
		}
	}
}

func jsonbExtractInputError(q string, bound bool, args ...any) fixture.Step {
	s := jsonTypeError(q, "22P02", "invalid input syntax for type json", 8, args...)
	s.Diagnostic.Detail = `Token "oops" is invalid.`
	s.Diagnostic.Context = "JSON data, line 1: oops"
	if bound {
		s.Diagnostic.Position = ""
		s.Diagnostic.Context += "\nunnamed portal parameter $1 = '...'"
	}
	return s
}

func jsonbExtractJSONKey(s string) string {
	data, _ := json.Marshal(s)
	return string(data)
}
