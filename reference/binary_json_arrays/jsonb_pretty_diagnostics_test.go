package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func jsonbPrettyInputError(q string, bound bool, args ...any) fixture.Step {
	s := jsonbExtractInputError(q, bound, args...)
	if !bound {
		s.Diagnostic.Position = fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, "'oops'")]) + 1)
	}
	return s
}

func TestReferenceJSONBPrettyDiagnostics(t *testing.T) {
	jsonTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ expr, signature string }{
			{`jsonb_pretty()`, `jsonb_pretty()`},
			{`jsonb_pretty(1)`, `jsonb_pretty(integer)`},
			{`jsonb_pretty('{}'::json)`, `jsonb_pretty(json)`},
			{`jsonb_pretty('{}'::text)`, `jsonb_pretty(text)`},
			{`jsonb_pretty('{}','{}')`, `jsonb_pretty(unknown, unknown)`},
			{`jsonb_pretty(wrong => '{}')`, `jsonb_pretty(wrong => unknown)`},
			{`"JSONB_PRETTY"('{}')`, `JSONB_PRETTY(unknown)`},
			{`jsonb_pretty(jsonb => '{}')`, `jsonb_pretty(jsonb => unknown)`},
		} {
			t.Run(tc.expr, func(t *testing.T) {
				for _, suffix := range []string{"", " FROM (SELECT 1 AS n) s WHERE false"} {
					jsonTypeAnalysisError(t, p, b, c, jsonTypeSignature("SELECT "+tc.expr+suffix, tc.signature, 8))
				}
			})
		}
		t.Run("explicit-variadic", func(t *testing.T) {
			jsonbPrettyBoth(t, p, b, c, jsonbPrettyOne(`SELECT jsonb_pretty(VARIADIC '{}'::jsonb) AS pretty`, "{\n}"))
		})
		for _, prefix := range []string{"SELECT ", "/*é雪*/ SELECT "} {
			for _, name := range []string{"jsonb_pretty", "pg_catalog.jsonb_pretty"} {
				for _, tc := range []struct{ tail, message string }{
					{`(DISTINCT '{}')`, "DISTINCT specified, but " + name + " is not an aggregate function"},
					{`('{}' ORDER BY 1)`, "ORDER BY specified, but " + name + " is not an aggregate function"},
					{`('{}') FILTER (WHERE true)`, "FILTER specified, but " + name + " is not an aggregate function"},
					{`('{}') OVER ()`, "OVER specified, but " + name + " is not a window function nor an aggregate function"},
				} {
					t.Run(prefix+name+tc.tail, func(t *testing.T) {
						jsonTypeAnalysisError(t, p, b, c, jsonTypeError(prefix+name+tc.tail, "42809", tc.message, utf8.RuneCountInString(prefix)+1))
					})
				}
			}
		}
		for _, tc := range []struct{ input, code, message, detail, context string }{
			{"oops", "22P02", "invalid input syntax for type json", `Token "oops" is invalid.`, "JSON data, line 1: oops"},
			{`{"x":"\u0000"}`, "22P05", "unsupported Unicode escape sequence", `\u0000 cannot be converted to text.`, `JSON data, line 1: {"x":"\u0000...`},
			{`{"x":"\uD800"}`, "22P02", "invalid input syntax for type json", "Unicode low surrogate must follow a high surrogate.", `JSON data, line 1: {"x":"\uD800"...`},
			{"1e131072", "22003", "value overflows numeric format", "", ""},
		} {
			t.Run(tc.input, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Detail, d.Context = tc.detail, tc.context
				d.Position = fixture.Position(21)
				s := fixture.ErrorDiagnostic("SELECT jsonb_pretty("+jsonTypeQuote(tc.input)+")", d)
				s.SelectRows, s.ErrorStage = true, "execute"
				jsonTypeAnalysisError(t, p, b, c, s)
				d = fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Detail, d.Context = tc.detail, tc.context
				if d.Context != "" {
					d.Context += "\n"
				}
				d.Context += "unnamed portal parameter $1 = '...'"
				s = fixture.ErrorDiagnostic("SELECT jsonb_pretty($1)", d, tc.input)
				s.SelectRows, s.ErrorStage = true, "execute"
				jsonTypeBoth(t, p, b, c, s)
				jsonbPrettyBoth(t, p, b, c, jsonbPrettyOne("SELECT jsonb_pretty($1) AS pretty", "[\n]", "[]"))
			})
		}
	})
}
