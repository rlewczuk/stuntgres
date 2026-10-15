package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func jsonbPathDeleteScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func jsonbPathDeleteSelect(q string, want any, args ...any) fixture.Step {
	return fixture.Select(q, []string{"v"}, []string{"JSONB"}, [][]any{{want}}, args...)
}

func jsonbPathDeleteError(q string, d fixture.Diagnostic, args ...any) fixture.Step {
	s := fixture.ErrorDiagnostic(q, d, args...)
	s.SelectRows, s.ErrorStage = true, "execute"
	return s
}

func TestReferenceJSONBPathDeleteValues(t *testing.T) {
	jsonbPathDeleteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ doc, path, want string }{
			{`["a",{"b":1}]`, `{1,b}`, `["a", {}]`},
			{`{"a":[{"x":1},{"x":2}],"keep":3}`, `{a,-1,x}`, `{"a": [{"x": 1}, {}], "keep": 3}`},
			{`{"a":[{"x":1},{"x":2}],"keep":3}`, `{a,-1}`, `{"a": [{"x": 1}], "keep": 3}`},
			{`[[1,2],[3,4]]`, `{-1,-2}`, `[[1, 2], [4]]`},
			{`{"-1":1,"0":2,"a":3}`, `{-1}`, `{"0": 2, "a": 3}`},
			{`{"a":null,"b":1.00}`, `{a}`, `{"b": 1.00}`},
			{`[1]`, `{0}`, `[]`}, {`[1]`, `{-1}`, `[]`}, {`{"a":1}`, `{a}`, `{}`},
			{`{"":1,"keep":2}`, `{""}`, `{"keep": 2}`},
			{`{"a,b":1,"keep":2}`, `{"a,b"}`, `{"keep": 2}`},
			{`{"\u00e9":1,"keep":2}`, `{é}`, `{"keep": 2}`},
			{`{"a":1,"a":2,"b":3}`, `{b}`, `{"a": 2}`},
			{`{"a\"b":1,"keep":2}`, `{"a\"b"}`, `{"keep": 2}`},
		} {
			t.Run(tc.doc+tc.path, func(t *testing.T) {
				q := "SELECT " + jsonTypeQuote(tc.doc) + "::jsonb #- " + jsonTypeQuote(tc.path) + "::text[] AS v"
				jsonTypeBoth(t, p, b, c, jsonbPathDeleteSelect(q, tc.want))
				jsonTypeBoth(t, p, b, c, jsonbPathDeleteSelect("SELECT $1::jsonb #- $2::text[] AS v", tc.want, tc.doc, tc.path))
			})
		}
		for _, args := range [][]any{{nil, "{a}"}, {`{"a":1}`, nil}, {nil, nil}} {
			jsonTypeBoth(t, p, b, c, jsonbPathDeleteSelect("SELECT $1::jsonb #- $2::text[] AS v", nil, args...))
		}
	})
}

func TestReferenceJSONBPathDeletePaths(t *testing.T) {
	jsonbPathDeleteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ doc, path, want string }{
			{`{"a":{"b":1}}`, `{missing,b}`, `{"a": {"b": 1}}`},
			{`{"a":{"b":1}}`, `{a,missing}`, `{"a": {"b": 1}}`},
			{`{"a":1}`, `{a,x,NULL}`, `{"a": 1}`},
			{`{"a":1}`, `{missing,NULL}`, `{"a": 1}`},
			{`{"a":1}`, `{}`, `{"a": 1}`}, {`[1,2]`, `{}`, `[1, 2]`},
			{`{}`, `{NULL}`, `{}`}, {`[]`, `{NULL}`, `[]`},
			{`[1,2]`, `{2}`, `[1, 2]`}, {`[1,2]`, `{-3}`, `[1, 2]`},
			{`[1,2]`, `{-2}`, `[2]`}, {`[1,2]`, `{-2147483648}`, `[1, 2]`},
			{`[1,2]`, `[-2:-2]={-1}`, `[1]`},
			{`[1,2]`, `{" +01"}`, `[1]`}, {`[1,2]`, "{\"\t-01\"}", `[1]`},
		} {
			t.Run(tc.doc+tc.path, func(t *testing.T) {
				q := "SELECT " + jsonTypeQuote(tc.doc) + "::jsonb #- " + jsonTypeQuote(tc.path) + "::text[] AS v"
				jsonTypeBoth(t, p, b, c, jsonbPathDeleteSelect(q, tc.want))
				jsonTypeBoth(t, p, b, c, jsonbPathDeleteSelect("SELECT $1::jsonb #- $2::text[] AS v", tc.want, tc.doc, tc.path))
			})
		}
	})
}
