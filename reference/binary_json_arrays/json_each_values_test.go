package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONEachValues(t *testing.T) {
	jsonEachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		const input = `{"a":1,"a":2,"\u0062":"x\n","empty":"","n":null,"s":"null","num":1.00e+02,"bool":true,"obj": { "z" : 1 },"arr":[1,  2]}`
		for _, f := range []string{"json_each", "json_each_text"} {
			t.Run(f, func(t *testing.T) {
				want := [][]any{{"a", "1"}, {"a", "2"}, {"b", `"x\n"`}, {"empty", `""`}, {"n", "null"}, {"s", `"null"`}, {"num", "1.00e+02"}, {"bool", "true"}, {"obj", `{ "z" : 1 }`}, {"arr", `[1,  2]`}}
				if f == "json_each_text" {
					want[2][1], want[3][1], want[4][1], want[5][1] = "x\n", "", nil, "null"
				}
				fixture.JSONEachBoth(t, p, b, c, jsonEachSelect(f, "SELECT key,value FROM "+f+"($1::json)", want, input))
				fixture.JSONEachBoth(t, p, b, c, jsonEachSelect(f, "SELECT * FROM "+f+"('"+input+"'::json)", want))
				for _, empty := range []any{`{}`, nil, []byte(nil)} {
					fixture.JSONEachBoth(t, p, b, c, jsonEachSelect(f, "SELECT * FROM "+f+"($1)", nil, empty))
				}
				for _, tc := range []struct {
					input string
					rows  [][]any
				}{
					{`{"":false,"neg":-0,"huge":1e999999}`, [][]any{{"", "false"}, {"neg", "-0"}, {"huge", "1e999999"}}},
					{`{"a":1,"\u0061":2}`, [][]any{{"a", "1"}, {"a", "2"}}},
				} {
					fixture.JSONEachBoth(t, p, b, c, jsonEachSelect(f, "SELECT * FROM "+f+"($1)", tc.rows, []byte(tc.input)))
				}
			})
		}
	})
}

func TestReferenceJSONEachUnicode(t *testing.T) {
	jsonEachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, f := range []string{"json_each", "json_each_text"} {
			value := any(`"\"\\\/\b\f\n\r\t\u00e9\ud83d\ude00"`)
			if f == "json_each_text" {
				value = "\"\\/\b\f\n\r\té😀"
			}
			fixture.JSONEachBoth(t, p, b, c, jsonEachSelect(f, "SELECT * FROM "+f+"($1)", [][]any{{"é😀", value}}, `{"\u00e9\ud83d\ude00":"\"\\\/\b\f\n\r\t\u00e9\ud83d\ude00"}`))
			// REL_18_0 each_worker enables escape decoding for BOTH variants,
			// including nested strings that are returned as raw source spans.
			for _, tc := range []struct{ input, code, message, detail, context string }{
				{`{"a":"\u0000"}`, "22P05", "unsupported Unicode escape sequence", `\u0000 cannot be converted to text.`, `JSON data, line 1: {"a":"\u0000...`},
				{`{"\u0000":1}`, "22P05", "unsupported Unicode escape sequence", `\u0000 cannot be converted to text.`, `JSON data, line 1: {"\u0000...`},
				{`{"a":{"b":"\u0000"}}`, "22P05", "unsupported Unicode escape sequence", `\u0000 cannot be converted to text.`, `JSON data, line 1: {"a":{"b":"\u0000...`},
				{`{"a":"\ud800"}`, "22P02", "invalid input syntax for type json", "Unicode low surrogate must follow a high surrogate.", `JSON data, line 1: {"a":"\ud800"...`},
				{`{"a":"\udc00"}`, "22P02", "invalid input syntax for type json", "Unicode low surrogate must follow a high surrogate.", `JSON data, line 1: {"a":"\udc00...`},
			} {
				t.Run(f+tc.input, func(t *testing.T) {
					d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					d.Detail, d.Context = tc.detail, tc.context
					for _, prepared := range []bool{false, true} {
						fixture.JSONKeysError(t, p, b, c, "SELECT * FROM "+f+"($1)", prepared, d, "execute", tc.input)
						fixture.JSONKeysError(t, p, b, c, "SELECT "+f+"('"+tc.input+"'::json)", prepared, d, "execute")
					}
				})
			}
		}
	})
}
