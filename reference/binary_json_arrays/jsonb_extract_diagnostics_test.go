package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func jsonbExtractDiagnostics(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	for _, tc := range []struct{ expr, signature string }{
		{"jsonb_extract_path()", "jsonb_extract_path()"},
		{"jsonb_extract_path('{}'::jsonb)", "jsonb_extract_path(jsonb)"},
		{"jsonb_extract_path_text('{}'::jsonb)", "jsonb_extract_path_text(jsonb)"},
		{"jsonb_extract_path(1,'a')", "jsonb_extract_path(integer, unknown)"},
		{"jsonb_extract_path('{}'::json,'a')", "jsonb_extract_path(json, unknown)"},
		{"jsonb_extract_path('{}'::jsonb,ARRAY['a'])", "jsonb_extract_path(jsonb, text[])"},
		{"jsonb_extract_path('{}'::jsonb,VARIADIC 'a'::text)", "jsonb_extract_path(jsonb, text)"},
		{"jsonb_extract_path('{}'::jsonb,wrong => 'a')", "jsonb_extract_path(jsonb, wrong => unknown)"},
		{"jsonb_extract_path('{}'::jsonb,1)", "jsonb_extract_path(jsonb, integer)"},
		{`"JSONB_EXTRACT_PATH"('{}','a')`, "JSONB_EXTRACT_PATH(unknown, unknown)"},
		{"jsonb_extract_path('{}',from_json => '{}',VARIADIC path_elems => '{a}'::text[])", "jsonb_extract_path(unknown, from_json => unknown, path_elems => text[])"},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			q := "SELECT " + tc.expr
			jsonTypeAnalysisError(t, p, b, c, jsonTypeSignature(q, tc.signature, 8))
		})
	}
	for _, tc := range []struct{ expr, signature, op string }{
		{"1 -> 'a'", "integer -> unknown", "->"},
		{"'{}'::jsonb -> 1::bigint", "jsonb -> bigint", "->"},
		{"'{}'::jsonb ->> 1::numeric", "jsonb ->> numeric", "->>"},
		{"'{}'::jsonb -> ARRAY['a']", "jsonb -> text[]", "->"},
		{"'{}'::jsonb #> 'a'::text", "jsonb #> text", "#>"},
		{"'{}'::jsonb #>> 1", "jsonb #>> integer", "#>>"},
		{"NULL::text -> NULL::text", "text -> text", "->"},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			q := "/*é*/ SELECT " + tc.expr
			s := jsonTypeError(q, "42883", "operator does not exist: "+tc.signature, len([]rune(q[:strings.Index(q, tc.op)]))+1)
			s.Diagnostic.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
			jsonTypeAnalysisError(t, p, b, c, s)
		})
	}
	// Unknown left stays ambiguous for every selected operator,
	// including parameterized forms; do not default the document family.
	for _, op := range []string{"->", "->>", "#>", "#>>"} {
		for _, lhs := range []string{"$1", "'{}'"} {
			for _, rhs := range []string{"'a'", "0"} {
				if strings.HasPrefix(op, "#") && rhs == "0" {
					continue
				}
				if strings.HasPrefix(op, "#") {
					rhs = "'{a}'"
				}
				t.Run(lhs+op+rhs, func(t *testing.T) {
					q := "SELECT " + lhs + " " + op + " " + rhs
					rtyp := "unknown"
					if rhs == "0" {
						rtyp = "integer"
					}
					s := jsonTypeError(q, "42725", "operator is not unique: unknown "+op+" "+rtyp, strings.Index(q, op)+1)
					s.Diagnostic.Hint = "Could not choose a best candidate operator. You might need to add explicit type casts."
					if lhs == "$1" {
						s.Args = []any{"{}"}
					}
					jsonTypeAnalysisError(t, p, b, c, s)
				})
			}
		}
	}
	for _, tc := range []struct{ q, code, msg, token string }{
		{`SELECT jsonb_extract_path(VARIADIC path_elems => '{a}'::text[],from_json => '{"a":7}'::jsonb)`, "42601", `syntax error at or near ","`, ","},
		{"SELECT jsonb_extract_path(from_json => '{}','a')", "42601", "positional argument cannot follow named argument", "'a'"},
		{"SELECT jsonb_extract_path(from_json => '{}',from_json => '{}')", "42601", `argument name "from_json" used more than once`, "from_json => '{}')"},
		{"SELECT jsonb_extract_path('{}','a') FILTER (WHERE true)", "42809", "FILTER specified, but jsonb_extract_path is not an aggregate function", "jsonb_extract_path"},
		{"SELECT jsonb_extract_path('{}','a') OVER ()", "42809", "OVER specified, but jsonb_extract_path is not a window function nor an aggregate function", "jsonb_extract_path"},
		{"SELECT jsonb_extract_path(DISTINCT '{}'::jsonb,'a')", "42809", "DISTINCT specified, but jsonb_extract_path is not an aggregate function", "jsonb_extract_path"},
		{"SELECT jsonb_extract_path('{}','a' ORDER BY 1)", "42809", "ORDER BY specified, but jsonb_extract_path is not an aggregate function", "jsonb_extract_path"},
	} {
		t.Run(tc.q, func(t *testing.T) {
			jsonTypeAnalysisError(t, p, b, c, jsonTypeError(tc.q, tc.code, tc.msg, strings.Index(tc.q, tc.token)+1))
		})
	}
	t.Run("argument_limit", func(t *testing.T) {
		q := "SELECT jsonb_extract_path('{}'" + strings.Repeat(",'a'", 100) + ")"
		jsonTypeAnalysisError(t, p, b, c, jsonTypeError(q, "54023", "cannot pass more than 100 arguments to a function", 8))
	})
	for _, tc := range []struct {
		q string
		s fixture.Step
	}{
		{"array", func() fixture.Step {
			q := "SELECT $1::jsonb #> $2::text[]"
			s := jsonTypeError(q, "22P02", `malformed array literal: "{a,,b}"`, 0, "{}", "{a,,b}")
			s.Diagnostic.Detail = `Unexpected "," character.`
			s.Diagnostic.Context = "unnamed portal parameter $2 = '...'"
			return s
		}()},
		{"integer", func() fixture.Step {
			q := "SELECT '{}'::jsonb -> 'bad'::int"
			return jsonTypeError(q, "22P02", `invalid input syntax for type integer: "bad"`, strings.Index(q, "'bad'")+1)
		}()},
		{"literal_precedence", jsonbExtractInputError("SELECT 'oops'::jsonb #> '{a,NULL}'", false)},
		{"parameter_precedence", jsonbExtractInputError("SELECT $1::jsonb #> $2::text[]", true, "oops", "{a,NULL}")},
		{"unicode_precedence", func() fixture.Step {
			s := jsonTypeError(`SELECT '{"x":"\u0000"}'::jsonb #> '{a,NULL}'`, "22P05", "unsupported Unicode escape sequence", 8)
			s.Diagnostic.Detail = `\u0000 cannot be converted to text.`
			s.Diagnostic.Context = `JSON data, line 1: {"x":"\u0000...`
			return s
		}()},
		{"bound_unicode_precedence", func() fixture.Step {
			s := jsonTypeError(`SELECT $1::jsonb #> $2::text[]`, "22P05", "unsupported Unicode escape sequence", 0, `{"x":"\u0000"}`, "{a,NULL}")
			s.Diagnostic.Detail = `\u0000 cannot be converted to text.`
			s.Diagnostic.Context = "JSON data, line 1: {\"x\":\"\\u0000...\nunnamed portal parameter $1 = '...'"
			return s
		}()},
	} {
		t.Run(tc.q, func(t *testing.T) {
			jsonTypeRun(t, p, b, c, tc.s)
			s := fixture.Prepared(tc.s)
			if len(s.Args) == 0 {
				s.ErrorStage = "prepare"
			}
			jsonTypeRun(t, p, b, c, s)
		})
	}
	jsonExtractBoth(t, p, b, c, jsonExtractOne(`SELECT '{"a":7}'::jsonb ->> 'a' AS v`, "TEXT", "7"))
}

func TestReferenceJSONBExtractDiagnostics(t *testing.T) {
	jsonbExtractScenario(t, jsonbExtractDiagnostics)
}
