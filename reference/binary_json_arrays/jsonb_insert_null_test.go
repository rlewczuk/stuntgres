package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func jsonbInsertCollision() fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic("22023", "cannot replace existing key")
	d.Hint = "Try using the function jsonb_set to replace key value."
	return d
}

func TestReferenceJSONBInsertNullAndDiagnostics(t *testing.T) {
	jsonbInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, args := range []string{
			`NULL,'{{a}}','2'`, `'1',NULL,'2'`, `'1','{{a}}',NULL`, `'1','{{a}}','2',NULL`,
			`'{"a":1}','{a}',NULL`, `'{"a":1}','{a}','2',NULL`,
			`NULL,ARRAY[NULL]::text[],'2'`, `'[]',ARRAY[NULL]::text[],NULL`,
		} {
			jsonbSetBoth(t, p, b, c, jsonbSetSelect("SELECT jsonb_insert("+args+") AS changed", nil))
		}
		jsonbSetBoth(t, p, b, c, jsonbSetSelect(`SELECT jsonb_insert('[0]','{0}','null') AS changed`, `[null, 0]`))
		for _, args := range []string{
			`'{"a":1}','{a}','2'`, `'{"a":null}','{a}','2',true`,
			`'{"a":1}','{a}','1'`, `'{"a":0,"a":1}','{a}','9'`,
		} {
			t.Run(args, func(t *testing.T) {
				jsonbSetBoth(t, p, b, c, jsonbSetError("SELECT jsonb_insert("+args+")", jsonbInsertCollision()))
			})
		}
		for _, tc := range []struct{ args, code, msg string }{
			{`'{}','{{a}}','2'`, "2202E", "wrong number of array subscripts"},
			{`'1','{{a}}','2'`, "2202E", "wrong number of array subscripts"},
			{`'1','{}','2'`, "22023", "cannot set path in scalar"},
			{`'true','{a}','2'`, "22023", "cannot set path in scalar"},
			{`'null','{a}','2'`, "22023", "cannot set path in scalar"},
			{`'"text"','{a}','2'`, "22023", "cannot set path in scalar"},
			{`'{}',ARRAY[NULL]::text[],'2'`, "22004", "path element at position 1 is null"},
			{`'{"a":1}',ARRAY['a',NULL]::text[],'2'`, "22004", "path element at position 2 is null"},
			{`'{"a":[]}',ARRAY['a',NULL]::text[],'2'`, "22004", "path element at position 2 is null"},
			{`'[]',ARRAY['bad'],'2'`, "22P02", `path element at position 1 is not an integer: "bad"`},
			{`'[[0]]',ARRAY['0','bad'],'2'`, "22P02", `path element at position 2 is not an integer: "bad"`},
		} {
			t.Run(tc.args, func(t *testing.T) {
				jsonbSetBoth(t, p, b, c, jsonbSetError("SELECT jsonb_insert("+tc.args+")", fixture.ControlsReferenceDiagnostic(tc.code, tc.msg)))
			})
		}
		for _, index := range []string{"", "1 ", "1x", "2147483648", "-2147483649"} {
			t.Run("index="+index, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("22P02", `path element at position 1 is not an integer: "`+index+`"`)
				jsonbSetBoth(t, p, b, c, jsonbSetError("SELECT jsonb_insert('[0,1]',ARRAY['"+index+"'],'9')", d))
			})
		}
	})
}
