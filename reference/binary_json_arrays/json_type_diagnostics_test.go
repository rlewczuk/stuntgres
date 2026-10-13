package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func jsonTypeDiagnostics(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, family := range []string{"json", "jsonb"} {
		for _, input := range []string{`{}`, `{"x":1}`, `0`, `""`, `"s"`, `true`, `false`, `null`} {
			message := "cannot get array length of a scalar"
			if input[0] == '{' {
				message = "cannot get array length of a non-array"
			}
			s := jsonTypeError("SELECT "+family+"_array_length("+jsonTypeQuote(input)+"::"+family+")", "22023", message, 0)
			// These are evaluation errors even for constant wrong-kind arguments.
			jsonTypeBoth(t, p, b, c, s)
		}
		for _, input := range []string{`[]`, `[1]`, `0`, `""`, `"s"`, `true`, `false`, `null`} {
			message := "cannot call " + family + "_object_keys on a scalar"
			if input[0] == '[' {
				message = "cannot call " + family + "_object_keys on an array"
			}
			jsonTypeBoth(t, p, b, c, jsonTypeError("SELECT "+family+"_object_keys("+jsonTypeQuote(input)+"::"+family+")", "22023", message, 0))
		}
	}
	// Complete decoder Context records are validated against pinned PostgreSQL 18.0.
	// Each late nested error must fail Query without publishing a partial cursor.
	for _, tc := range []struct{ input, code, detail, context string }{
		{`{"x":"\u0000"}`, "22P05", `\u0000 cannot be converted to text.`, `JSON data, line 1: {"x":"\u0000...`},
		{`{"\u0000":1}`, "22P05", `\u0000 cannot be converted to text.`, `JSON data, line 1: {"\u0000...`},
		{`{"ok":1,"nested":{"x":"\u0000"}}`, "22P05", `\u0000 cannot be converted to text.`, `JSON data, line 1: {"ok":1,"nested":{"x":"\u0000...`},
		{`"\u0000"`, "22P05", `\u0000 cannot be converted to text.`, `JSON data, line 1: "\u0000...`},
		{`{"x":"\uD800"}`, "22P02", "Unicode low surrogate must follow a high surrogate.", `JSON data, line 1: {"x":"\uD800"...`},
		{`{"x":"\uDC00"}`, "22P02", "Unicode low surrogate must follow a high surrogate.", `JSON data, line 1: {"x":"\uDC00...`},
		{`{"x":"\uD800\uD800"}`, "22P02", "Unicode high surrogate must not follow a high surrogate.", `JSON data, line 1: {"x":"\uD800\uD800...`},
	} {
		message := "invalid input syntax for type json"
		if tc.code == "22P05" {
			message = "unsupported Unicode escape sequence"
		}
		for _, bound := range []bool{false, true} {
			q := "SELECT json_object_keys(" + jsonTypeQuote(tc.input) + "::json)"
			var args []any
			if bound {
				q, args = "SELECT json_object_keys($1)", []any{tc.input}
			}
			s := jsonTypeError(q, tc.code, message, 0, args...)
			s.Diagnostic.Detail, s.Diagnostic.Context = tc.detail, tc.context
			jsonTypeBoth(t, p, b, c, s)
		}
	}
	jsonTypeBoth(t, p, b, c, jsonTypeError(`SELECT json_object_keys('["\u0000"]'::json)`, "22023", "cannot call json_object_keys on an array", 0))
	for _, bound := range []bool{false, true} {
		q := "SELECT json_typeof('oops')"
		var args []any
		if bound {
			q, args = "SELECT json_typeof($1)", []any{"oops"}
		}
		s := jsonTypeError(q, "22P02", "invalid input syntax for type json", 20, args...)
		s.Diagnostic.Detail = `Token "oops" is invalid.`
		s.Diagnostic.Context = "JSON data, line 1: oops"
		if bound {
			s.Diagnostic.Position = ""
			s.Diagnostic.Context += "\nunnamed portal parameter $1 = '...'"
		}
		jsonTypeRun(t, p, b, c, s)
		s = fixture.Prepared(s)
		if !bound {
			s.ErrorStage = "prepare"
		}
		jsonTypeRun(t, p, b, c, s)
	}
}

func TestReferenceJSONTypeDiagnostics(t *testing.T) { jsonTypeScenario(t, jsonTypeDiagnostics) }
