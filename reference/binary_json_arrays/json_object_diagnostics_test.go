package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONObjectDiagnostics(t *testing.T) {
	jsonObjectScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ args, code, msg string }{
			{`'{a,b,c}'`, "2202E", "array must have even number of elements"},
			{`'{NULL,x,y}'`, "2202E", "array must have even number of elements"},
			{`'{{a},{b}}'`, "2202E", "array must have two columns"},
			{`'{{NULL,x,y},{d,e,f}}'`, "2202E", "array must have two columns"},
			{`'{{{NULL,b}}}'`, "2202E", "wrong number of array subscripts"},
			{`'{{a,b}}','{{1,2}}'`, "2202E", "wrong number of array subscripts"},
			{`'{{NULL,a}}','{x}'`, "2202E", "wrong number of array subscripts"},
			{`'{a}','{{x}}'`, "2202E", "wrong number of array subscripts"},
			{`'{}','{x}'`, "2202E", "wrong number of array subscripts"},
			{`'{x}','{}'`, "2202E", "wrong number of array subscripts"},
			{`'{NULL,a}','{x}'`, "2202E", "mismatched array dimensions"},
			{`'{a}','{x,y}'`, "2202E", "mismatched array dimensions"},
			{`'{NULL,v}'`, "22004", "null value not allowed for object key"},
			{`'{{a,x},{NULL,y}}'`, "22004", "null value not allowed for object key"},
			{`'{a,NULL}','{x,y}'`, "22004", "null value not allowed for object key"},
		} {
			t.Run(tc.args, func(t *testing.T) {
				s := jsonObjectError("SELECT json_object("+tc.args+")", tc.code, tc.msg, "execute")
				jsonObjectBoth(t, p, b, c, s)
			})
		}
		for _, prefix := range []string{"", "/*é*/ "} {
			t.Run("codec "+prefix, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("22P02", `malformed array literal: "{a,,b}"`)
				d.Detail = `Unexpected "," character.`
				d.Position = "20"
				if prefix != "" {
					d.Position = "26"
				}
				s := fixture.ErrorDiagnostic(prefix+"SELECT json_object('{a,,b}')", d)
				s.SelectRows = true
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				s.Prepared = true
				s.ErrorStage = "prepare"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			})
		}
		for _, tc := range []struct{ q, input, detail, position string }{
			{`SELECT json_object('{{a},{b,c}}')`, `{{a},{b,c}}`, "Multidimensional arrays must have sub-arrays with matching dimensions.", "20"},
			{`SELECT json_object(NULL::text[],'{a,,b}'::text[])`, `{a,,b}`, `Unexpected "," character.`, "33"},
			{`SELECT json_object('{a,,b}'::text[],NULL::text[])`, `{a,,b}`, `Unexpected "," character.`, "20"},
		} {
			t.Run(tc.q, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("22P02", `malformed array literal: "`+tc.input+`"`)
				d.Detail = tc.detail
				d.Position = tc.position
				s := fixture.ErrorDiagnostic(tc.q, d)
				s.SelectRows = true
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				s.Prepared = true
				s.ErrorStage = "prepare"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			})
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE jo_demand(a text[])", 0))
		for _, q := range []string{
			"SELECT json_object('{a,b,c}') FROM jo_demand",
			"SELECT json_object('{a,b,c}') WHERE FALSE",
		} {
			t.Run(q, func(t *testing.T) {
				jsonObjectBoth(t, p, b, c, jsonObjectError(q, "2202E", "array must have even number of elements", "execute"))
			})
		}
		jsonObjectBoth(t, p, b, c, fixture.Select("SELECT json_object(a) AS v FROM jo_demand", []string{"v"}, []string{"JSON"}, nil))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO jo_demand VALUES('{a,b,c}')", 1))
		jsonObjectBoth(t, p, b, c, jsonObjectError("SELECT json_object(a) FROM jo_demand", "2202E", "array must have even number of elements", "execute"))
	})
}

func TestReferenceJSONObjectTyping(t *testing.T) {
	jsonObjectScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE DOMAIN jo_text_array AS text[]", 0))
		for _, expr := range []string{
			`pg_catalog.json_object('{a,1}')`, `"json_object"('{a,1}')`, `JSON_OBJECT('{a,1}')`,
			`json_object('{a,1}'::varchar[])`, `json_object('{a,1}'::char(1)[])`,
			`json_object('{a,1}'::jo_text_array)`, `json_object('{a}'::varchar[],'{1}'::varchar[])`,
			`pg_catalog.json_object(VARIADIC ARRAY['a','1']::text[])`,
			`pg_catalog.json_object(ARRAY['a']::text[],VARIADIC ARRAY['1']::text[])`,
		} {
			jsonObjectBoth(t, p, b, c, jsonObjectSelect("SELECT "+expr+" AS v", `{"a" : "1"}`))
		}
		jsonObjectBoth(t, p, b, c, fixture.Select("SELECT json_object FROM (SELECT 7 AS json_object) AS x", []string{"json_object"}, []string{"INT4"}, [][]any{{int64(7)}}))
		for _, tc := range []struct{ call, signature string }{
			{`json_object(1)`, `pg_catalog.json_object(integer)`},
			{`json_object('a'::text)`, `pg_catalog.json_object(text)`},
			{`json_object(ARRAY[1,2])`, `pg_catalog.json_object(integer[])`},
			{`pg_catalog.json_object()`, `pg_catalog.json_object()`},
			{`"JSON_OBJECT"('{a,1}')`, `JSON_OBJECT(unknown)`},
			{`json_object('{}','{}','{}')`, `pg_catalog.json_object(unknown, unknown, unknown)`},
			{`pg_catalog.json_object(a => ARRAY['a','1'])`, `pg_catalog.json_object(a => text[])`},
		} {
			t.Run(tc.call, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("42883", "function "+tc.signature+" does not exist")
				d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				d.Position = "8"
				s := fixture.ErrorDiagnostic("SELECT "+tc.call, d)
				s.SelectRows = true
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				s.Prepared = true
				s.ErrorStage = "prepare"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			})
		}
	})
}
