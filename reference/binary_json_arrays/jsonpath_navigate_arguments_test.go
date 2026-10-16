package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONPathNavigateArguments(t *testing.T) {
	jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			expr string
			want any
		}{
			{`jsonb_path_query_array('[1,2]','$[*]')`, `[1, 2]`},
			{`jsonb_path_query_array('[1,2]','$[*]','{}')`, `[1, 2]`},
			{`jsonb_path_query_array('[1,2]','$[*]','{}',false)`, `[1, 2]`},
			{`jsonb_path_query_array(path => '$[*]', target => '[1,2]')`, `[1, 2]`},
			{`jsonb_path_query_array(path := '$[*]', target := '[1,2]')`, `[1, 2]`},
			{`jsonb_path_query_array('[{}, {"x":2}]','strict $[*].x',silent => true)`, `[]`},
			{`pg_catalog."jsonb_path_query_array"('null','null')`, `[null]`},
			{`jsonb_path_query_array(NULL::jsonb,'$','[]',true)`, nil},
			{`jsonb_path_query_array('1',NULL::jsonpath,'[]',true)`, nil},
			{`jsonb_path_query_array('1','$',NULL::jsonb,true)`, nil},
			{`jsonb_path_query_array('1','$','[]',NULL::boolean)`, nil},
			{`jsonb_path_query_array(jsonb_path_query_array('[1,2]','$[*]'),'$[1]')`, `[2]`},
			{`CASE WHEN true THEN jsonb_path_query_array(NULL::jsonb,'strict $.x') ELSE '[]'::jsonb END`, nil},
		} {
			jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateSelect("SELECT "+tc.expr+" AS changed", tc.want))
		}
		for _, tc := range []struct {
			q    string
			args []any
			want any
		}{
			{`SELECT jsonb_path_query_array($1,'$[*]') AS changed`, []any{`[1,2]`}, `[1, 2]`},
			{`SELECT jsonb_path_query_array('[1,2]',$1) AS changed`, []any{`$[1]`}, `[2]`},
			{`SELECT jsonb_path_query_array('0','$v',$1) AS changed`, []any{`{"v":3}`}, `[3]`},
			{`SELECT jsonb_path_query_array('{}','strict $.x','{}',$1) AS changed`, []any{true}, `[]`},
			{`SELECT jsonb_path_query_array($1,'$v',$1) AS changed`, []any{`{"v":4}`}, `[4]`},
		} {
			jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateSelect(tc.q, tc.want, tc.args...))
		}
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec(`CREATE DOMAIN jpn_doc AS jsonb CHECK(VALUE <> '0'::jsonb)`, 0))
		run(fixture.Exec(`CREATE DOMAIN jpn_path AS jsonpath`, 0))
		jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateSelect(`SELECT jsonb_path_query_array('[1,2]'::jpn_doc,'$[*]'::jpn_path) AS changed`, `[1, 2]`))
		d := fixture.ControlsReferenceDiagnostic("23514", `value for domain jpn_doc violates check constraint "jpn_doc_check"`)
		d.Schema, d.DataType, d.Constraint = "public", "jpn_doc", "jpn_doc_check"
		s := fixture.ErrorDiagnostic(`SELECT jsonb_path_query_array('0'::jpn_doc,'$',silent => true)`, d)
		s.SelectRows = true
		jsonpathNavigateBoth(t, p, b, c, s)
		run(fixture.Exec(`CREATE TABLE jpn_null(v jsonb,b text)`, 0))
		run(fixture.Exec(`INSERT INTO jpn_null VALUES(NULL,'bad')`, 1))
		jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateError(`SELECT jsonb_path_query_array(v,'$','{}',b::boolean) FROM jpn_null`, "22P02", `invalid input syntax for type boolean: "bad"`))
		for _, tc := range []struct{ call, signature string }{
			{`jsonb_path_query_array('1')`, `jsonb_path_query_array(unknown)`},
			{`jsonb_path_query_array('1','$','{}',false,false)`, `jsonb_path_query_array(unknown, unknown, unknown, boolean, boolean)`},
			{`jsonb_path_query_array('1'::text,'$')`, `jsonb_path_query_array(text, unknown)`},
			{`jsonb_path_query_array('1'::json,'$')`, `jsonb_path_query_array(json, unknown)`},
			{`jsonb_path_query_array('1','$'::text)`, `jsonb_path_query_array(unknown, text)`},
			{`jsonb_path_query_array(target => '1', wrong => '$')`, `jsonb_path_query_array(target => unknown, wrong => unknown)`},
			{`"JSONB_PATH_QUERY_ARRAY"('1','$')`, `JSONB_PATH_QUERY_ARRAY(unknown, unknown)`},
		} {
			d := fixture.ControlsReferenceDiagnostic("42883", "function "+tc.signature+" does not exist")
			d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			d.Position = "8"
			s := fixture.ErrorDiagnostic("SELECT "+tc.call, d)
			s.SelectRows = true
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			s = fixture.Prepared(s)
			s.ErrorStage = "prepare"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		for _, tc := range []struct{ call, code, message, token string }{
			{`jsonb_path_query_array(target=>'1',target=>'2',path=>'$')`, "42601", `argument name "target" used more than once`, `target=>'2'`},
			{`jsonb_path_query_array(target=>'1','$')`, "42601", `positional argument cannot follow named argument`, `'$'`},
			{`jsonb_path_query_array(DISTINCT '1','$')`, "42809", `DISTINCT specified, but jsonb_path_query_array is not an aggregate function`, `jsonb_path_query_array`},
			{`jsonb_path_query_array('1','$' ORDER BY 1)`, "42809", `ORDER BY specified, but jsonb_path_query_array is not an aggregate function`, `jsonb_path_query_array`},
			{`jsonb_path_query_array('1','$') FILTER (WHERE true)`, "42809", `FILTER specified, but jsonb_path_query_array is not an aggregate function`, `jsonb_path_query_array`},
			{`jsonb_path_query_array('1','$') OVER ()`, "42809", `OVER specified, but jsonb_path_query_array is not a window function nor an aggregate function`, `jsonb_path_query_array`},
		} {
			q := "SELECT " + tc.call
			d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
			d.Position = fixture.Position(strings.Index(q, tc.token) + 1)
			s := fixture.ErrorDiagnostic(q, d)
			s.SelectRows = true
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			s = fixture.Prepared(s)
			s.ErrorStage = "prepare"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
	})
}
