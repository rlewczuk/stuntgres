package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonbObjectScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
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

func jsonbObjectSelect(q string, want any, args ...any) fixture.Step {
	return fixture.Select(q, []string{"v"}, []string{"JSONB"}, [][]any{{want}}, args...)
}

// Expectations come from PostgreSQL 18 Table 9.49 and REL_18_0 jsonb.c
// (jsonb_object/jsonb_object_two_arg), with canonical ordering and last-key
// selection from jsonb_util.c (lengthCompareJsonbPair/uniqueifyJsonbObject).
// Expectations are independently specified, not learned from native output.
func TestReferenceJSONBObjectValues(t *testing.T) {
	jsonbObjectScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			expr string
			want any
		}{
			{`jsonb_object('{a,1,b,def,c,3.5}')`, `{"a": "1", "b": "def", "c": "3.5"}`},
			{`jsonb_object('{{a,1},{b,def},{c,3.5}}'::text[])`, `{"a": "1", "b": "def", "c": "3.5"}`},
			{`jsonb_object(ARRAY['a','1','b','2'])`, `{"a": "1", "b": "2"}`},
			{`jsonb_object(ARRAY[['a','1'],['b','2']])`, `{"a": "1", "b": "2"}`},
			{`jsonb_object('{a,b}'::text[],'{1,2}'::text[])`, `{"a": "1", "b": "2"}`},
			{`jsonb_object('[-2:1]={a,1,b,2}'::text[])`, `{"a": "1", "b": "2"}`},
			{`jsonb_object('[-1:0][3:4]={{a,1},{b,2}}'::text[])`, `{"a": "1", "b": "2"}`},
			{`jsonb_object('[-2:-1]={a,b}'::text[],'[7:8]={1,2}'::text[])`, `{"a": "1", "b": "2"}`},
			{`jsonb_object(ARRAY['a',NULL,'a','two','',''])`, `{"": "", "a": "two"}`},
			{`jsonb_object(ARRAY['a','a',''],ARRAY[NULL,'two',''])`, `{"": "", "a": "two"}`},
			{`jsonb_object(ARRAY['é','one','z','two','aa','three','a','first','a',NULL])`, `{"a": null, "z": "two", "aa": "three", "é": "one"}`},
			{`jsonb_object(ARRAY['é','z','aa','a','a'],ARRAY['one','two','three','first',NULL])`, `{"a": null, "z": "two", "aa": "three", "é": "one"}`},
			{`jsonb_object(ARRAY['','first','','last','a','1e131072','b','3.5'])`, `{"": "last", "a": "1e131072", "b": "3.5"}`},
			{`jsonb_object(ARRAY['a','b','c'],ARRAY['true','null','{"x":1}'])`, `{"a": "true", "b": "null", "c": "{\"x\":1}"}`},
			{`jsonb_object(ARRAY['z','true','a','null','n','NULL','j','{"x":1}'])`, `{"a": "null", "j": "{\"x\":1}", "n": "NULL", "z": "true"}`},
			{`jsonb_object(ARRAY[E'"\\',E'\b\f\n\r\t\001','é😀','<>&/'])`, `{"\"\\": "\b\f\n\r\t\u0001", "é😀": "<>&/"}`},
			{`jsonb_object(ARRAY['é😀'],ARRAY['<>&/'])`, `{"é😀": "<>&/"}`},
			{`jsonb_object('{}'::text[])`, `{}`},
			{`jsonb_object('{{},{}}'::text[])`, `{}`},
			{`jsonb_object(ARRAY[]::text[])`, `{}`},
			{`jsonb_object('{}'::text[],'{}'::text[])`, `{}`},
			{`jsonb_object(NULL::text[])`, nil},
			{`jsonb_object(NULL,NULL)`, nil},
			{`jsonb_object(NULL::text[],'{{a,b}}'::text[])`, nil},
			{`jsonb_object('{NULL}'::text[],NULL::text[])`, nil},
		} {
			t.Run(tc.expr, func(t *testing.T) { jsonObjectBoth(t, p, b, c, jsonbObjectSelect("SELECT "+tc.expr+" AS v", tc.want)) })
		}
		jsonObjectBoth(t, p, b, c, fixture.Select(
			`SELECT jsonb_object(ARRAY['b','2','a','old','a','1']) = '{"a":"1","b":"2"}'::jsonb AS equal`,
			[]string{"equal"}, []string{"BOOL"}, [][]any{{true}}))
	})
}
