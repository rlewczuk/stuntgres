package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonExtractUnicodeError(q, input string, args ...any) fixture.Step {
	code, message, detail, context := "22P05", "unsupported Unicode escape sequence", `\u0000 cannot be converted to text.`, `JSON data, line 1: {"x":"\u0000...`
	switch input {
	case `{"x":"\uD800"}`:
		code, message, detail, context = "22P02", "invalid input syntax for type json", "Unicode low surrogate must follow a high surrogate.", `JSON data, line 1: {"x":"\uD800"...`
	case `{"x":"\uDC00"}`:
		code, message, detail, context = "22P02", "invalid input syntax for type json", "Unicode low surrogate must follow a high surrogate.", `JSON data, line 1: {"x":"\uDC00...`
	case `{"x":"\uD800\uD800"}`:
		code, message, detail, context = "22P02", "invalid input syntax for type json", "Unicode high surrogate must not follow a high surrogate.", `JSON data, line 1: {"x":"\uD800\uD800...`
	case `{"\u0000":1}`:
		context = `JSON data, line 1: {"\u0000...`
	case `{"ok":1,"nested":{"x":"\u0000"}}`:
		context = `JSON data, line 1: {"ok":1,"nested":{"x":"\u0000...`
	}
	s := jsonTypeError(q, code, message, 0, args...)
	s.Diagnostic.Detail, s.Diagnostic.Context = detail, context
	return s
}

func jsonExtractUnicode(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	jsonExtractBoth(t, p, b, c, jsonExtractOne(`SELECT '{"\u0061":"\uD834\uDD1E","é":"雪","𝄞":7}'::json ->> 'a' AS v`, "TEXT", "𝄞"))
	jsonExtractBoth(t, p, b, c, jsonExtractOne(`SELECT '{"é":"雪","𝄞":7}'::json ->> 'é' AS v`, "TEXT", "雪"))
	jsonExtractPathForms(t, p, b, c, `{"é":{"𝄞":7}}`, "{é,𝄞}", []string{"é", "𝄞"}, "7", "7")
	for _, input := range []string{`{"x":"\u0000"}`, `{"\u0000":1}`, `{"ok":1,"nested":{"x":"\u0000"}}`, `{"x":"\uD800"}`, `{"x":"\uDC00"}`, `{"x":"\uD800\uD800"}`} {
		for _, expr := range []string{"$1::json -> 'missing'", "$1::json ->> 'missing'", "$1::json #> '{missing}'", "$1::json #>> '{missing}'", "json_extract_path($1,'missing')", "json_extract_path_text($1,VARIADIC '{missing}'::text[])"} {
			t.Run(input+expr, func(t *testing.T) {
				jsonTypeBoth(t, p, b, c, jsonExtractUnicodeError("SELECT "+expr+" AS v", input, input))
			})
		}
		// Any NULL path component suppresses decoding, even after an impossible prefix.
		for _, q := range []string{"SELECT $1::json #> '{missing,NULL}' AS v", "SELECT json_extract_path($1,'missing',NULL) AS v", "SELECT json_extract_path($1,VARIADIC '{missing,NULL}'::text[]) AS v"} {
			jsonExtractBoth(t, p, b, c, jsonExtractOne(q, "JSON", nil, input))
		}
	}
	q := `SELECT '{"x":"\u0000"}'::json -> 'missing' AS v`
	jsonTypeBoth(t, p, b, c, jsonExtractUnicodeError(q, `{"x":"\u0000"}`))
	q = `SELECT '{"x":"\u0000"}'::json -> 'missing' AS v FROM (SELECT 1) s WHERE false`
	jsonTypeBoth(t, p, b, c, jsonExtractUnicodeError(q, `{"x":"\u0000"}`))
	jsonExtractBoth(t, p, b, c, jsonExtractOne(`SELECT '{"a":7}'::json ->> 'a' AS v`, "TEXT", "7"))
}
func TestReferenceJSONExtractUnicode(t *testing.T) { jsonExtractScenario(t, jsonExtractUnicode) }
