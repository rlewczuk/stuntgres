package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonbSetRaise() fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic("22004", "JSON value must not be null")
	d.Detail = `Exception was raised because null_value_treatment is "raise_exception".`
	d.Hint = "To avoid, either change the null_value_treatment argument or ensure that an SQL NULL is not passed."
	return d
}

func jsonbSetError(q string, d fixture.Diagnostic, args ...any) fixture.Step {
	s := fixture.ErrorDiagnostic(q, d, args...)
	s.SelectRows = true
	s.ErrorStage = "execute"
	return s
}

func TestReferenceJSONBSetNullModes(t *testing.T) {
	jsonbSetScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			args string
			want any
		}{
			{`'{"a":1,"b":2}','{a}',NULL`, `{"a": null, "b": 2}`},
			{`'{"a":1,"b":2}','{a}',NULL,true,'use_json_null'`, `{"a": null, "b": 2}`},
			{`'{"a":1,"b":2}','{a}',NULL,false,'delete_key'`, `{"b": 2}`},
			{`'{"a":1,"b":2}','{a}',NULL,true,'return_target'`, `{"a": 1, "b": 2}`},
			{`'[1,2,3]','{-1}',NULL,true,'delete_key'`, `[1, 2]`},
			{`'[1,2,3]','{3}',NULL,true,'delete_key'`, `[1, 2, 3]`},
			{`'{}','{a}',NULL,false,'use_json_null'`, `{}`},
			{`'{}','{a}',NULL,true,'delete_key'`, `{}`},
			{`'{"a":1}','{a}','null',true,'INVALID'`, `{"a": null}`},
			{`'{"a":1}','{a}','2',true,'USE_JSON_NULL'`, `{"a": 2}`},
			{`'1','{{a}}',NULL,true,'return_target'`, `1`},
			{`'1',ARRAY[NULL]::text[],NULL,true,'return_target'`, `1`},
			{`NULL,'{{a}}',NULL,true,'raise_exception'`, nil},
			{`'1',NULL,NULL,true,'INVALID'`, nil},
			{`'1','{{a}}',NULL,NULL,NULL`, nil},
			{`NULL,'{a}','2',true,NULL`, nil},
			{`'[]',ARRAY[NULL]::text[],NULL,true,'delete_key'`, `[]`},
			{`'{}',ARRAY[NULL]::text[],NULL,false,'use_json_null'`, `{}`},
		} {
			jsonbSetBoth(t, p, b, c, jsonbSetSelect("SELECT jsonb_set_lax("+tc.args+") AS changed", tc.want))
		}
		for _, args := range []string{`NULL,'{{a}}','2'`, `'1',NULL,'2'`, `'1','{{a}}',NULL`, `'1','{{a}}','2',NULL`} {
			jsonbSetBoth(t, p, b, c, jsonbSetSelect("SELECT jsonb_set("+args+") AS changed", nil))
		}
		for _, args := range []string{`'{"a":1}','{a}',NULL,true,'raise_exception'`, `'1','{{a}}',NULL,true,'raise_exception'`, `'{}',ARRAY[NULL]::text[],NULL,true,'raise_exception'`} {
			jsonbSetBoth(t, p, b, c, jsonbSetError("SELECT jsonb_set_lax("+args+")", jsonbSetRaise()))
		}
		for _, args := range []string{`'{}','{a}',NULL,true,'INVALID'`, `'{}','{a}',NULL,true,'USE_JSON_NULL'`, `'{}','{a}','2',true,NULL`, `'{}','{a}',NULL,true,NULL`} {
			d := fixture.ControlsReferenceDiagnostic("22023", `null_value_treatment must be "delete_key", "return_target", "use_json_null", or "raise_exception"`)
			jsonbSetBoth(t, p, b, c, jsonbSetError("SELECT jsonb_set_lax("+args+")", d))
		}
	})
}
