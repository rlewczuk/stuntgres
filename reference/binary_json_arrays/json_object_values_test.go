package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonObjectScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
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

func jsonObjectBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}

func jsonObjectSelect(q string, want any, args ...any) fixture.Step {
	return fixture.Select(q, []string{"v"}, []string{"JSON"}, [][]any{{want}}, args...)
}

func jsonObjectError(q, code, message, stage string, args ...any) fixture.Step {
	s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic(code, message), args...)
	s.SelectRows = true
	s.ErrorStage = stage
	return s
}

func TestReferenceJSONObjectValues(t *testing.T) {
	jsonObjectScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			expr string
			want any
		}{
			{`json_object('{a,1,b,def,c,3.5}')`, `{"a" : "1", "b" : "def", "c" : "3.5"}`},
			{`json_object('{{a,1},{b,def},{c,3.5}}'::text[])`, `{"a" : "1", "b" : "def", "c" : "3.5"}`},
			{`json_object(ARRAY['a','1','b','2'])`, `{"a" : "1", "b" : "2"}`},
			{`json_object(ARRAY[['a','1'],['b','2']])`, `{"a" : "1", "b" : "2"}`},
			{`json_object('{a,b}'::text[],'{1,2}'::text[])`, `{"a" : "1", "b" : "2"}`},
			{`json_object('[-2:1]={a,1,b,2}'::text[])`, `{"a" : "1", "b" : "2"}`},
			{`json_object('[-1:0][3:4]={{a,1},{b,2}}'::text[])`, `{"a" : "1", "b" : "2"}`},
			{`json_object('[-2:-1]={a,b}'::text[],'[7:8]={1,2}'::text[])`, `{"a" : "1", "b" : "2"}`},
			{`json_object(ARRAY['a',NULL,'a','two','',''])`, `{"a" : null, "a" : "two", "" : ""}`},
			{`json_object(ARRAY['a','a',''],ARRAY[NULL,'two',''])`, `{"a" : null, "a" : "two", "" : ""}`},
			{`json_object(ARRAY['z','true','a','null','n','NULL','j','{"x":1}'])`, `{"z" : "true", "a" : "null", "n" : "NULL", "j" : "{\"x\":1}"}`},
			{`json_object(ARRAY[E'"\\',E'\b\f\n\r\t\001','é😀','<>&/'])`, `{"\"\\" : "\b\f\n\r\t\u0001", "é😀" : "<>&/"}`},
			{`json_object(ARRAY['é😀'],ARRAY['<>&/'])`, `{"é😀" : "<>&/"}`},
			{`json_object('{}'::text[])`, `{}`},
			{`json_object('{{},{}}'::text[])`, `{}`},
			{`json_object(ARRAY[]::text[])`, `{}`},
			{`json_object('{}'::text[],'{}'::text[])`, `{}`},
			{`json_object(NULL::text[])`, nil},
			{`json_object(NULL,NULL)`, nil},
			{`json_object(NULL::text[],'{{a,b}}'::text[])`, nil},
			{`json_object('{NULL}'::text[],NULL::text[])`, nil},
		} {
			t.Run(tc.expr, func(t *testing.T) { jsonObjectBoth(t, p, b, c, jsonObjectSelect("SELECT "+tc.expr+" AS v", tc.want)) })
		}
	})
}
