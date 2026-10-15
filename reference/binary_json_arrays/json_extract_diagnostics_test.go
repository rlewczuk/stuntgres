package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonExtractDiagnostics(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	for _, tc := range []struct{ expr, signature string }{
		{"json_extract_path()", "json_extract_path()"},
		{"json_extract_path('{}'::json)", "json_extract_path(json)"},
		{"json_extract_path_text('{}'::json)", "json_extract_path_text(json)"},
		{"json_extract_path(1,'a')", "json_extract_path(integer, unknown)"},
		{"json_extract_path('{}'::json,ARRAY['a'])", "json_extract_path(json, text[])"},
		{"json_extract_path('{}'::json,VARIADIC 'a'::text)", "json_extract_path(json, text)"},
		{"json_extract_path('{}'::json,wrong => 'a')", "json_extract_path(json, wrong => unknown)"},
		{"json_extract_path('{}'::json,1)", "json_extract_path(json, integer)"},
		{"json_extract_path('{}',from_json => '{}',VARIADIC path_elems => '{a}'::text[])", "json_extract_path(unknown, from_json => unknown, path_elems => text[])"},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			q := "SELECT " + tc.expr
			jsonTypeAnalysisError(t, p, b, c, jsonTypeSignature(q, tc.signature, 8))
		})
	}
	for _, tc := range []struct{ expr, signature, token string }{
		{"1 -> 'a'", "integer -> unknown", "->"},
		{"'{}'::json -> 1::bigint", "json -> bigint", "->"},
		{"'{}'::json #> 'a'::text", "json #> text", "#>"},
		{"NULL::text -> NULL::text", "text -> text", "->"},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			q := "/*é*/ SELECT " + tc.expr
			d := fixture.ControlsReferenceDiagnostic("42883", "operator does not exist: "+tc.signature)
			d.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
			d.Position = fixture.Position(len([]rune(q[:strings.Index(q, tc.token)])) + 1)
			s := fixture.ErrorDiagnostic(q, d)
			s.SelectRows, s.ErrorStage = true, "execute"
			jsonTypeAnalysisError(t, p, b, c, s)
		})
	}
	for _, expr := range []string{"'{}' -> 'a'", "'{}' ->> 'a'", "'{}' #> '{a}'"} {
		t.Run(expr, func(t *testing.T) {
			q := "SELECT " + expr
			op := "->"
			if strings.Contains(expr, "->>") {
				op = "->>"
			}
			if strings.Contains(expr, "#>") {
				op = "#>"
			}
			s := jsonTypeError(q, "42725", "operator is not unique: unknown "+op+" unknown", strings.Index(q, op)+1)
			s.Diagnostic.Hint = "Could not choose a best candidate operator. You might need to add explicit type casts."
			jsonTypeAnalysisError(t, p, b, c, s)
		})
	}
	for _, tc := range []struct{ q, code, msg, token string }{
		{`SELECT json_extract_path(VARIADIC path_elems => '{a}'::text[],from_json => '{"a":7}'::json)`, "42601", `syntax error at or near ","`, ","},
		{"SELECT json_extract_path(from_json => '{}','a')", "42601", "positional argument cannot follow named argument", "'a'"},
		{"SELECT json_extract_path('{}','a') FILTER (WHERE true)", "42809", "FILTER specified, but json_extract_path is not an aggregate function", "json_extract_path"},
		{"SELECT json_extract_path('{}','a') OVER ()", "42809", "OVER specified, but json_extract_path is not a window function nor an aggregate function", "json_extract_path"},
		{"SELECT json_extract_path(DISTINCT '{}'::json,'a')", "42809", "DISTINCT specified, but json_extract_path is not an aggregate function", "json_extract_path"},
		{"SELECT json_extract_path('{}','a' ORDER BY 1)", "42809", "ORDER BY specified, but json_extract_path is not an aggregate function", "json_extract_path"},
	} {
		t.Run(tc.q, func(t *testing.T) {
			s := jsonTypeError(tc.q, tc.code, tc.msg, strings.Index(tc.q, tc.token)+1)
			jsonTypeAnalysisError(t, p, b, c, s)
		})
	}
	q := "SELECT json_extract_path('{}'" + strings.Repeat(",'a'", 100) + ")"
	jsonTypeAnalysisError(t, p, b, c, jsonTypeError(q, "54023", "cannot pass more than 100 arguments to a function", 8))
	for _, tc := range []struct {
		q, code, msg, detail, context string
		pos                           int
		args                          []any
	}{
		{"SELECT $1::json #> $2::text[]", "22P02", `malformed array literal: "{a,,b}"`, `Unexpected "," character.`, "unnamed portal parameter $2 = '...'", 0, []any{"{}", "{a,,b}"}},
		{"SELECT '{}'::json -> 'bad'::int", "22P02", `invalid input syntax for type integer: "bad"`, "", "", 22, nil},
		{"SELECT 'oops'::json -> 'a'", "22P02", "invalid input syntax for type json", `Token "oops" is invalid.`, "JSON data, line 1: oops", 8, nil},
	} {
		t.Run(tc.q, func(t *testing.T) {
			s := jsonTypeError(tc.q, tc.code, tc.msg, tc.pos, tc.args...)
			s.Diagnostic.Detail, s.Diagnostic.Context = tc.detail, tc.context
			jsonTypeRun(t, p, b, c, s)
			s = fixture.Prepared(s)
			if len(tc.args) == 0 {
				s.ErrorStage = "prepare"
			}
			jsonTypeRun(t, p, b, c, s)
		})
	}
}
func TestReferenceJSONExtractDiagnostics(t *testing.T) {
	jsonExtractScenario(t, jsonExtractDiagnostics)
}
