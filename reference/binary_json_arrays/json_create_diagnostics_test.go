package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONCreateDiagnostics(t *testing.T) {
	jsonCreateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE DOMAIN jc_key_array AS int[]`, 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE TYPE jc_key_pair AS (n int)`, 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE DOMAIN jc_key_row AS jc_key_pair`, 0))
		for _, name := range []string{"json_build_object", "jsonb_build_object"} {
			for _, args := range []string{`'a'`, `NULL`, `'a',1,'b'`, `VARIADIC ARRAY['a']`} {
				d := fixture.ControlsReferenceDiagnostic("22023", "argument list must have even number of elements")
				d.Hint = "The arguments of " + name + "() must consist of alternating keys and values."
				t.Run(name+args, func(t *testing.T) { jsonCreateError(t, p, b, c, "SELECT "+name+"("+args+") AS v", d, false) })
			}
			for _, args := range []string{`NULL,1`, `'ok',1,NULL,2`, `VARIADIC ARRAY[NULL,'x']`} {
				d := fixture.ControlsReferenceDiagnostic("22004", "null value not allowed for object key")
				if name == "jsonb_build_object" {
					d.Code = "22023"
					d.Message = "argument 1: key must not be null"
					if args == `'ok',1,NULL,2` {
						d.Message = "argument 3: key must not be null"
					}
				}
				t.Run(name+args, func(t *testing.T) { jsonCreateError(t, p, b, c, "SELECT "+name+"("+args+") AS v", d, false) })
			}
			for _, key := range []string{`ARRAY[1]`, `ROW(1)`, `'1'::json`, `'1'::jsonb`, `ARRAY[1]::jc_key_array`, `ROW(1)::jc_key_row`} {
				d := fixture.ControlsReferenceDiagnostic("22023", "key value must be scalar, not array, composite, or json")
				t.Run(name+key, func(t *testing.T) { jsonCreateError(t, p, b, c, "SELECT "+name+"("+key+",1) AS v", d, false) })
			}
		}
		for _, name := range []string{"to_json", "to_jsonb", "array_to_json"} {
			for _, input := range []string{`'x'`, `NULL`} {
				d := fixture.ControlsReferenceDiagnostic("42804", "could not determine polymorphic type because input has type unknown")
				t.Run(name+input, func(t *testing.T) { jsonCreateError(t, p, b, c, "SELECT "+name+"("+input+") AS v", d, true) })
			}
		}
		for _, tc := range []struct{ call, signature string }{
			{`array_to_json(1)`, `array_to_json(integer)`}, {`row_to_json(1)`, `row_to_json(integer)`},
			{`to_json()`, `to_json()`}, {`to_jsonb(1,2)`, `to_jsonb(integer, integer)`},
			{`array_to_json(ARRAY[1],1)`, `array_to_json(integer[], integer)`},
			{`row_to_json(ROW(1),1)`, `row_to_json(record, integer)`},
			{`"TO_JSON"(1)`, `TO_JSON(integer)`},
			{`json_build_array(1,VARIADIC ARRAY[2])`, `json_build_array(integer, integer[])`},
			{`jsonb_build_object('a',VARIADIC ARRAY[1])`, `jsonb_build_object(unknown, integer[])`},
			{`to_json(a => 1)`, `to_json(a => integer)`},
			{`pg_catalog.to_json()`, `pg_catalog.to_json()`},
		} {
			for _, prefix := range []string{"SELECT ", "/*é😀*/ SELECT "} {
				d := fixture.ControlsReferenceDiagnostic("42883", "function "+tc.signature+" does not exist")
				d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				d.Position = fixture.Position(len([]rune(prefix)) + 1)
				t.Run(prefix+tc.call, func(t *testing.T) { jsonCreateError(t, p, b, c, prefix+tc.call, d, true) })
			}
		}
		for _, name := range []string{"json_build_array", "jsonb_build_array", "json_build_object", "jsonb_build_object"} {
			d := fixture.ControlsReferenceDiagnostic("42804", "VARIADIC argument must be an array")
			d.Position = fixture.Position(len([]rune("SELECT "+name+"(VARIADIC ")) + 1)
			t.Run(name+" scalar variadic", func(t *testing.T) { jsonCreateError(t, p, b, c, "SELECT "+name+"(VARIADIC 1)", d, true) })
		}
		for _, tc := range []struct{ q, msg string }{
			{`SELECT to_json(DISTINCT 1)`, `DISTINCT specified, but to_json is not an aggregate function`},
			{`SELECT to_json(1 ORDER BY 1)`, `ORDER BY specified, but to_json is not an aggregate function`},
			{`SELECT to_json(1) FILTER (WHERE TRUE)`, `FILTER specified, but to_json is not an aggregate function`},
			{`SELECT to_json(1) OVER ()`, `OVER specified, but to_json is not a window function nor an aggregate function`},
			{`SELECT json_build_array(*)`, `json_build_array(*) specified, but json_build_array is not an aggregate function`},
		} {
			t.Run(tc.q, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("42809", tc.msg)
				d.Position = "8"
				jsonCreateError(t, p, b, c, tc.q, d, true)
			})
		}
		d := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type boolean: "notbool"`)
		d.Position = fixture.Position(len([]rune("SELECT array_to_json(ARRAY[1],")) + 1)
		jsonCreateError(t, p, b, c, `SELECT array_to_json(ARRAY[1],'notbool')`, d, true)
		for _, tc := range jsonbReferenceInvalidCases() {
			if tc.name != "unicode_zero" && tc.name != "lone_low" && tc.name != "magnitude_overflow" {
				continue
			}
			// These JSON values are valid JSON source; only conversion to JSONB fails.
			for _, call := range []string{"to_jsonb(%s)", "jsonb_build_array(%s)", "jsonb_build_object('a',%s,'a',1)"} {
				for _, bound := range []bool{false, true} {
					operand := jsonReferenceQuote(tc.input) + "::json"
					var args []any
					if bound {
						operand = "$1::json"
						args = []any{tc.input}
					}
					q := "SELECT " + strings.Replace(call, "%s", operand, 1) + " AS v"
					t.Run(q+tc.name, func(t *testing.T) { jsonCreateError(t, p, b, c, q, tc.want, false, args...) })
				}
			}
			jsonObjectBoth(t, p, b, c, jsonCreateSelect("SELECT json_build_array("+jsonReferenceQuote(tc.input)+"::json) AS v", "JSON", "["+tc.input+"]"))
		}
		for _, count := range []int{100, 101} {
			args := strings.TrimSuffix(strings.Repeat("1,", count), ",")
			q := "SELECT json_build_array(" + args + ") AS v"
			if count == 100 {
				jsonObjectBoth(t, p, b, c, jsonCreateSelect(q, "JSON", "["+strings.TrimSuffix(strings.Repeat("1, ", count), ", ")+"]"))
			} else {
				d := fixture.ControlsReferenceDiagnostic("54023", "cannot pass more than 100 arguments to a function")
				d.Position = "8"
				jsonCreateError(t, p, b, c, q, d, true)
			}
		}
	})
}
