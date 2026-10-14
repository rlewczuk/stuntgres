package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBSetTyping(t *testing.T) {
	jsonbSetScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonbSetTypingEdges(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{"CREATE DOMAIN jbs_doc AS jsonb", "CREATE DOMAIN jbs_path AS text[]", "CREATE DOMAIN jbs_flag AS boolean", "CREATE DOMAIN jbs_mode AS text"} {
			run(fixture.Exec(q, 0))
		}
		for _, call := range []string{
			`pg_catalog.jsonb_set('{"a":1}','{a}','2')`,
			`"jsonb_set"('{"a":1}','{a}','2')`,
			`JSONB_SET('{"a":1}','{a}','2',true)`,
			`jsonb_set('{"a":1}'::json::jsonb,'{a}'::varchar[],'2')`,
			`jsonb_set('{"a":1}'::jbs_doc,'{a}'::jbs_path,'2'::jbs_doc,true::jbs_flag)`,
			`jsonb_set(replacement => '2', path => '{a}', jsonb_in => '{"a":1}')`,
			`jsonb_set('{"a":1}',replacement := '2',path := '{a}')`,
			`jsonb_set_lax(replacement => '2',jsonb_in => '{"a":1}',path => '{a}',null_value_treatment => 'return_target')`,
			`jsonb_set_lax('{"a":1}','{a}','2',true,'use_json_null'::jbs_mode)`,
			`pg_catalog.jsonb_set('{"a":1}','{a}',VARIADIC '2'::jsonb)`,
		} {
			t.Run(call, func(t *testing.T) { jsonbSetBoth(t, p, b, c, jsonbSetSelect("SELECT "+call+" AS changed", `{"a": 2}`)) })
		}
		jsonbSetBoth(t, p, b, c, jsonbSetSelect(`SELECT jsonb_set_lax('{"a":1}',path => '{a}',replacement => NULL,null_value_treatment => 'delete_key') AS changed`, `{}`))
		for _, tc := range []struct{ call, sig string }{
			{`jsonb_set()`, `jsonb_set()`},
			{`jsonb_set('{}','{a}')`, `jsonb_set(unknown, unknown)`},
			{`jsonb_set('{}','{a}','2',true,true)`, `jsonb_set(unknown, unknown, unknown, boolean, boolean)`},
			{`jsonb_set(1,'{a}','2')`, `jsonb_set(integer, unknown, unknown)`},
			{`jsonb_set('{}'::json,'{a}','2')`, `jsonb_set(json, unknown, unknown)`},
			{`jsonb_set('{}',ARRAY[1],'2')`, `jsonb_set(unknown, integer[], unknown)`},
			{`jsonb_set('{}','{a}','2',1)`, `jsonb_set(unknown, unknown, unknown, integer)`},
			{`jsonb_set_lax('{}','{a}',NULL,true,1)`, `jsonb_set_lax(unknown, unknown, unknown, boolean, integer)`},
			{`"JSONB_SET"('{}','{a}','2')`, `JSONB_SET(unknown, unknown, unknown)`},
			{`jsonb_set(target => '{}',path => '{a}',replacement => '2')`, `jsonb_set(target => unknown, path => unknown, replacement => unknown)`},
			{`jsonb_set('{}','{a}',VARIADIC ARRAY['2']::jsonb[])`, `jsonb_set(unknown, unknown, jsonb[])`},
			{`jsonb_set(*)`, `jsonb_set()`},
		} {
			t.Run(tc.call, func(t *testing.T) {
				for _, prefix := range []string{"SELECT ", "/*é*/ SELECT "} {
					d := fixture.ControlsReferenceDiagnostic("42883", "function "+tc.sig+" does not exist")
					d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
					d.Position = fixture.Position(len([]rune(prefix)) + 1)
					s := jsonbSetError(prefix+tc.call, d)
					run(s)
					s.Prepared = true
					s.ErrorStage = "prepare"
					run(s)
				}
			})
		}
		for _, tc := range []struct{ call, code, msg, token string }{
			{`jsonb_set(jsonb_in => '{}',path => '{a}',path => '{b}',replacement => '2')`, "42601", `argument name "path" used more than once`, "path => '{b}'"},
			{`jsonb_set(jsonb_in => '{}','{a}','2')`, "42601", "positional argument cannot follow named argument", "'{a}'"},
			{`jsonb_set(DISTINCT '{}','{a}','2')`, "42809", "DISTINCT specified, but jsonb_set is not an aggregate function", "jsonb_set"},
			{`jsonb_set('{}','{a}','2' ORDER BY 1)`, "42809", "ORDER BY specified, but jsonb_set is not an aggregate function", "jsonb_set"},
			{`jsonb_set('{}','{a}','2') FILTER (WHERE true)`, "42809", "FILTER specified, but jsonb_set is not an aggregate function", "jsonb_set"},
			{`jsonb_set('{}','{a}','2') OVER ()`, "42809", "OVER specified, but jsonb_set is not a window function nor an aggregate function", "jsonb_set"},
			{`jsonb_set_lax(NULL,'{a}',NULL,true,missing)`, "42703", `column "missing" does not exist`, "missing"},
		} {
			t.Run(tc.call, func(t *testing.T) {
				q := "/*é*/ SELECT " + tc.call
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.msg)
				d.Position = fixture.Position(len([]rune(q[:strings.Index(q, tc.token)])) + 1)
				s := jsonbSetError(q, d)
				run(s)
				s.Prepared = true
				s.ErrorStage = "prepare"
				run(s)
			})
		}
	})
}
