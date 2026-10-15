package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONElementsUnicode(t *testing.T) {
	jsonElementsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonKeysBoth(t, p, b, c, jsonElementsSelect("json_array_elements", "SELECT * FROM json_array_elements($1)", [][]any{{`"\u0000"`}, {`"\ud800"`}, {`{"x":"\u0000"}`}}, `["\u0000","\ud800",{"x":"\u0000"}]`))
		for _, tc := range []struct{ input, code, message, detail, context string }{
			{`["\u0000"]`, "22P05", "unsupported Unicode escape sequence", `\u0000 cannot be converted to text.`, `JSON data, line 1: ["\u0000...`},
			{`["\ud800"]`, "22P02", "invalid input syntax for type json", `Unicode low surrogate must follow a high surrogate.`, `JSON data, line 1: ["\ud800"...`},
			{`["\ud800\ud800"]`, "22P02", "invalid input syntax for type json", `Unicode high surrogate must not follow a high surrogate.`, `JSON data, line 1: ["\ud800\ud800...`},
			{`["\udc00"]`, "22P02", "invalid input syntax for type json", `Unicode low surrogate must follow a high surrogate.`, `JSON data, line 1: ["\udc00...`},
			{`["\ud800\u0061"]`, "22P02", "invalid input syntax for type json", `Unicode low surrogate must follow a high surrogate.`, `JSON data, line 1: ["\ud800\u0061...`},
			{`[{"\u0000":1}]`, "22P05", "unsupported Unicode escape sequence", `\u0000 cannot be converted to text.`, `JSON data, line 1: [{"\u0000...`},
			{`[{"x":"\u0000"}]`, "22P05", "unsupported Unicode escape sequence", `\u0000 cannot be converted to text.`, `JSON data, line 1: [{"x":"\u0000...`},
			{`["ok",{"late":"\u0000"}]`, "22P05", "unsupported Unicode escape sequence", `\u0000 cannot be converted to text.`, `JSON data, line 1: ["ok",{"late":"\u0000...`},
		} {
			t.Run(tc.input, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Detail, d.Context = tc.detail, tc.context
				for _, suffix := range []string{"", " LIMIT 1"} {
					jsonElementsError(t, p, b, c, "SELECT * FROM json_array_elements_text($1)"+suffix, d, false, tc.input)
				}
				// The same valid JSON input does not decode strings in raw mode.
				// Each input here has one immediate value except the late-failure case.
				if tc.input != `["ok",{"late":"\u0000"}]` {
					jsonKeysBoth(t, p, b, c, jsonElementsSelect("json_array_elements", "SELECT * FROM json_array_elements($1)", [][]any{{tc.input[1 : len(tc.input)-1]}}, tc.input))
				}
			})
		}
		jsonKeysBoth(t, p, b, c, jsonElementsSelect("json_array_elements_text", "SELECT * FROM json_array_elements_text($1)", [][]any{{"𝄞"}, {`{"x":"\ud834\udd1e"}`}}, `["\ud834\udd1e",{"x":"\ud834\udd1e"}]`))
	})
}
