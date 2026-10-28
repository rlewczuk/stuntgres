package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONRecordDiagnostics(t *testing.T) {
	jsonRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		// Complete English records and execution stages are pinned by tests-3,
		// the root's real PostgreSQL18.0 observation, not native output.
		for _, f := range []string{"json_populate_record", "json_to_record"} {
			for _, input := range []string{`[]`, `1`, `"x"`, `true`, `null`} {
				q := `SELECT * FROM ` + f + `($1::json) AS r(a int,b text)`
				if f == "json_populate_record" {
					q = `SELECT * FROM ` + f + `(NULL::jr_pair,$1::json)`
				}
				message := "cannot call populate_composite on a scalar"
				if input == "[]" {
					message = "cannot call populate_composite on an array"
				}
				t.Run(f+"/"+input, func(t *testing.T) {
					jsonRecordErrorBoth(t, p, b, c, q, jsonRecordDiagnostic("22023", message, 0), false, input)
				})
			}
		}
		for _, f := range []string{"json_populate_recordset", "json_to_recordset"} {
			for _, input := range []string{`{}`, `1`, `"x"`, `null`, `[null]`, `[1]`, `[[]]`, `[{},false]`} {
				q := `SELECT * FROM ` + f + `($1::json) AS r(a int,b text)`
				if f == "json_populate_recordset" {
					q = `SELECT * FROM ` + f + `(NULL::jr_pair,$1::json)`
				}
				message := "cannot call " + f + " on a scalar"
				if input == "{}" {
					message = "cannot call " + f + " on an object"
				} else if input[0] == '[' {
					message = "argument of " + f + " must be an array of objects"
				}
				t.Run(f+"/"+input, func(t *testing.T) {
					jsonRecordErrorBoth(t, p, b, c, q, jsonRecordDiagnostic("22023", message, 0), false, input)
				})
			}
		}
		array := jsonRecordDiagnostic("22P02", "expected JSON array", 0)
		array.Hint = `See the value of key "a".`
		mixed := array
		mixed.Hint = `See the array element [1] of key "a".`
		ragged := jsonRecordDiagnostic("22P02", "malformed JSON array", 0)
		ragged.Detail = "Multidimensional arrays must have sub-arrays with matching dimensions."
		badInteger := jsonRecordDiagnostic("22P02", `invalid input syntax for type integer: "bad"`, 0)
		overflow := jsonRecordDiagnostic("22003", "numeric field overflow", 0)
		overflow.Detail = "A field with precision 3, scale 1 must round to an absolute value less than 10^2."
		unicode := jsonRecordDiagnostic("22P05", "unsupported Unicode escape sequence", 0)
		unicode.Detail = `\u0000 cannot be converted to text.`
		unicode.Context = `JSON data, line 1: {"ignored":"\u0000...`
		surrogate := jsonRecordDiagnostic("22P02", "invalid input syntax for type json", 0)
		surrogate.Detail = "Unicode low surrogate must follow a high surrogate."
		surrogate.Context = `JSON data, line 1: {"ignored":"\ud800"...`
		malformed := jsonRecordDiagnostic("22P02", "invalid input syntax for type json", 0)
		malformed.Detail = "The input string ended unexpectedly."
		malformed.Context = "JSON data, line 1: {\"a\":\nunnamed portal parameter $1 = '...'"
		for _, tc := range []struct {
			name, q, input string
			want           fixture.Diagnostic
		}{
			{"array-number", `SELECT * FROM json_to_record($1::json) AS r(a int[])`, `{"a":1}`, array},
			{"array-object", `SELECT * FROM json_to_record($1::json) AS r(a int[])`, `{"a":{}}`, array},
			{"array-mixed", `SELECT * FROM json_to_record($1::json) AS r(a int[])`, `{"a":[[1],2]}`, mixed},
			{"array-ragged", `SELECT * FROM json_to_record($1::json) AS r(a int[])`, `{"a":[[1],[2,3]]}`, ragged},
			{"array-child", `SELECT * FROM json_to_record($1::json) AS r(a int[])`, `{"a":[1,"bad"]}`, badInteger},
			{"nested-shape", `SELECT * FROM json_to_record($1::json) AS r(a jr_sub)`, `{"a":[1,2]}`, jsonRecordDiagnostic("22023", "cannot call populate_composite on an array", 0)},
			{"nested-integer", `SELECT * FROM json_to_record($1::json) AS r(a jr_sub)`, `{"a":{"d":"bad"}}`, badInteger},
			{"invalid-integer", `SELECT * FROM json_to_record($1::json) AS r(a int)`, `{"a":"bad"}`, badInteger},
			{"overflow", `SELECT * FROM json_to_record($1::json) AS r(a int)`, `{"a":2147483648}`, jsonRecordDiagnostic("22003", `value "2147483648" is out of range for type integer`, 0)},
			{"typmod", `SELECT * FROM json_to_record($1::json) AS r(a varchar(2))`, `{"a":"abc"}`, jsonRecordDiagnostic("22001", "value too long for type character varying(2)", 0)},
			{"numeric-overflow", `SELECT * FROM json_to_record($1::json) AS r(a numeric(3,1))`, `{"a":100}`, overflow},
			{"descriptor-order", `SELECT * FROM json_to_record($1::json) AS r(a int,b int)`, `{"b":"bad-b","a":"bad-a"}`, jsonRecordDiagnostic("22P02", `invalid input syntax for type integer: "bad-a"`, 0)},
			{"duplicate-last-invalid", `SELECT * FROM json_to_record($1::json) AS r(a int)`, `{"a":2,"a":"bad"}`, badInteger},
			{"ignored-unicode", `SELECT * FROM json_to_record($1::json) AS r(a int)`, `{"ignored":"\u0000","a":1}`, unicode},
			{"ignored-surrogate", `SELECT * FROM json_to_record($1::json) AS r(a int)`, `{"ignored":"\ud800","a":1}`, surrogate},
			{"malformed-json", `SELECT * FROM json_to_record($1::json) AS r(a int)`, `{"a":`, malformed},
			{"late-row", `SELECT * FROM json_to_recordset($1::json) AS r(a int)`, `[{"a":1},{"a":"bad"}]`, badInteger},
		} {
			t.Run(tc.name, func(t *testing.T) { jsonRecordErrorBoth(t, p, b, c, tc.q, tc.want, false, tc.input) })
		}
	})
}
