package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBSetPathDiagnostics(t *testing.T) {
	jsonbSetScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ args, code, msg string }{
			{`'{}','{{a,b}}','2'`, "2202E", "wrong number of array subscripts"},
			{`'1','{{a,b}}','2'`, "2202E", "wrong number of array subscripts"},
			{`'1','{}','2'`, "22023", "cannot set path in scalar"},
			{`'null','{a}','2'`, "22023", "cannot set path in scalar"},
			{`'{"a":1}',ARRAY['a',NULL]::text[],'2'`, "22004", "path element at position 2 is null"},
			{`'[1]',ARRAY[NULL]::text[],'2'`, "22004", "path element at position 1 is null"},
			{`'{"a":[1]}','[-2:-1]={a,x}'::text[],'2'`, "22P02", `path element at position 2 is not an integer: "x"`},
			{`'[]','{bad}','2'`, "22P02", `path element at position 1 is not an integer: "bad"`},
		} {
			t.Run(tc.args, func(t *testing.T) {
				for _, f := range []string{"jsonb_set", "jsonb_set_lax"} {
					jsonbSetBoth(t, p, b, c, jsonbSetError("/*é*/ SELECT "+f+"("+tc.args+")", fixture.ControlsReferenceDiagnostic(tc.code, tc.msg)))
				}
			})
		}
		for _, index := range []string{"", "1 ", "1x", "2147483648", "-2147483649", "+"} {
			t.Run("index="+index, func(t *testing.T) {
				q := "SELECT jsonb_set('[1]',ARRAY['" + index + "'],'9')"
				d := fixture.ControlsReferenceDiagnostic("22P02", `path element at position 1 is not an integer: "`+index+`"`)
				jsonbSetBoth(t, p, b, c, jsonbSetError(q, d))
			})
		}
		for _, tc := range []struct{ args, code, msg string }{
			{`'1','{{a}}',NULL,true,'delete_key'`, "2202E", "wrong number of array subscripts"},
			{`'1','{}',NULL,true,'delete_key'`, "22023", "cannot delete path in scalar"},
			{`'[1]',ARRAY[NULL]::text[],NULL,true,'delete_key'`, "22004", "path element at position 1 is null"},
		} {
			jsonbSetBoth(t, p, b, c, jsonbSetError("SELECT jsonb_set_lax("+tc.args+")", fixture.ControlsReferenceDiagnostic(tc.code, tc.msg)))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE jbs_demand(v jsonb,p text[],m text)", 0))
		jsonbSetBoth(t, p, b, c, fixture.Select("SELECT jsonb_set(v,p,'9') AS changed FROM jbs_demand", []string{"changed"}, []string{"JSONB"}, nil))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO jbs_demand VALUES('[1]','{x}','bad')`, 1))
		jsonbSetBoth(t, p, b, c, jsonbSetError("SELECT jsonb_set(v,p,'9') FROM jbs_demand", fixture.ControlsReferenceDiagnostic("22P02", `path element at position 1 is not an integer: "x"`)))
		// The row-owned mode expression must execute even with a SQL NULL target.
		jsonbSetBoth(t, p, b, c, jsonbSetError("SELECT jsonb_set_lax(NULL::jsonb,p,NULL,true,(m::integer)::text) FROM jbs_demand", fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)))
	})
}
