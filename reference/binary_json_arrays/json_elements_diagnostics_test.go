package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func jsonElementsError(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, q string, d fixture.Diagnostic, analysis bool, args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		stage := "execute"
		if prepared && analysis {
			stage = "prepare"
		}
		jsonKeysError(t, p, b, c, q, prepared, d, stage, args...)
	}
}

func TestReferenceJSONElementsTyping(t *testing.T) {
	jsonElementsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE DOMAIN jelements_document AS json", 0))
		for _, f := range []string{"json_array_elements", "json_array_elements_text"} {
			for _, call := range []string{f + "('[1]')", f + "($1)", f + "($1::json)", "pg_catalog." + f + "($1)", f + "(from_json => $1)", f + "(from_json := $1)", f + "($1::jelements_document)", f + "(VARIADIC $1::json)", f + "(VARIADIC from_json => $1::json)"} {
				args := []any{"[1]"}
				if strings.Contains(call, "'[1]'") {
					args = nil
				}
				jsonKeysBoth(t, p, b, c, jsonElementsSelect(f, "SELECT * FROM "+call, [][]any{{"1"}}, args...))
			}
			jsonKeysBoth(t, p, b, c, jsonElementsSelect(f, "SELECT * FROM "+f+"(NULL)", nil))
			for _, tc := range []struct{ call, signature string }{
				{f + "()", f + "()"}, {f + "('[]','[]')", f + "(unknown, unknown)"},
				{f + "('[]'::text)", f + "(text)"}, {f + "(1)", f + "(integer)"},
				{f + "('[]'::jsonb)", f + "(jsonb)"}, {f + "(ARRAY['[]']::json[])", f + "(json[])"},
				{f + "(wrong => '[]'::json)", f + "(wrong => json)"},
				{"pg_catalog." + f + "(VARIADIC ARRAY['[]']::json[])", "pg_catalog." + f + "(json[])"},
			} {
				for _, prefix := range []string{"SELECT ", "SELECT * FROM ", "/*é*/ SELECT "} {
					d := fixture.ControlsReferenceDiagnostic("42883", "function "+tc.signature+" does not exist")
					d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
					d.Position = fixture.Position(len([]rune(prefix)) + 1)
					t.Run(prefix+tc.call, func(t *testing.T) { jsonElementsError(t, p, b, c, prefix+tc.call, d, true) })
				}
			}
			for _, prefix := range []string{"SELECT ", "SELECT * FROM ", "/*é*/ SELECT "} {
				d := fixture.ControlsReferenceDiagnostic("3F000", `schema "jelements_missing" does not exist`)
				d.Position = fixture.Position(len([]rune(prefix)) + 1)
				jsonElementsError(t, p, b, c, prefix+"jelements_missing."+f+"('[]'::json)", d, true)
			}
		}
	})
}

func jsonElementsRootErrors(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, f := range []string{"json_array_elements", "json_array_elements_text"} {
		for _, input := range []string{`{}`, `{"a":1}`, `null`, `true`, `false`, `1`, `"x"`} {
			kind := "scalar"
			if strings.HasPrefix(input, "{") {
				kind = "non-array"
			}
			d := fixture.ControlsReferenceDiagnostic("22023", "cannot call "+f+" on a "+kind)
			for _, prefix := range []string{"SELECT ", "SELECT * FROM ", "/*é*/ SELECT "} {
				jsonElementsError(t, p, b, c, prefix+f+"($1)", d, false, input)
				jsonElementsError(t, p, b, c, prefix+f+"('"+input+"'::json)", d, false)
			}
		}
	}
}

func TestReferenceJSONElementsDiagnostics(t *testing.T) {
	jsonElementsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonElementsRootErrors(t, p, b, c)
		for _, f := range []string{"json_array_elements", "json_array_elements_text"} {
			for _, prefix := range []string{"SELECT ", "/*é*/ SELECT "} {
				q := prefix + f + "('{\"a\":}'::json)"
				d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type json")
				d.Detail, d.Context = `Expected JSON value, but found "}".`, `JSON data, line 1: {"a":}`
				d.Position = fixture.Position(len([]rune(prefix)) + len(f) + 2)
				jsonElementsError(t, p, b, c, q, d, true)
				d.Position = ""
				d.Context += "\nunnamed portal parameter $2 = '...'"
				jsonElementsError(t, p, b, c, prefix+"$1::integer,"+f+"($2)", d, false, 7, `{"a":}`)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE jelements_guard_"+f+"(j json)", 0))
			for _, tc := range []struct {
				q, clause string
				analysis  bool
			}{
				{"VALUES(" + f + "('[]'))", "VALUES", true},
				{"CREATE TABLE jelements_default_" + f + "(j json DEFAULT " + f + "('[]'))", "DEFAULT expressions", false},
				{"CREATE TABLE jelements_check_" + f + "(j json CHECK(" + f + "(j) IS NULL))", "check constraints", false},
				{"CREATE TABLE jelements_generated_" + f + "(j json,v json GENERATED ALWAYS AS (" + f + "(j)) STORED)", "column generation expressions", false},
				{"CREATE INDEX jelements_index_" + f + " ON jelements_guard_" + f + " ((" + f + "(j)))", "index expressions", false},
			} {
				d := fixture.ControlsReferenceDiagnostic("0A000", "set-returning functions are not allowed in "+tc.clause)
				d.Position = fixture.Position(strings.LastIndex(tc.q, f+"(") + 1)
				jsonElementsError(t, p, b, c, tc.q, d, tc.analysis)
			}
			for _, name := range []string{f, "pg_catalog." + f} {
				for _, decoration := range []string{"DISTINCT", "ORDER BY", "FILTER", "OVER"} {
					call := name + "('[]'::json)"
					message := decoration + " specified, but " + name + " is not an aggregate function"
					switch decoration {
					case "DISTINCT":
						call = name + "(DISTINCT '[]'::json)"
					case "ORDER BY":
						call = name + "('[]'::json ORDER BY 1)"
					case "FILTER":
						call += " FILTER (WHERE true)"
					case "OVER":
						call += " OVER ()"
						message = "OVER specified, but " + name + " is not a window function nor an aggregate function"
					}
					d := fixture.ControlsReferenceDiagnostic("42809", message)
					d.Position = "14"
					jsonElementsError(t, p, b, c, "/*é*/ SELECT "+call, d, true)
				}
			}
			const hint = "You might be able to move the set-returning function into a LATERAL FROM item."
			for _, tc := range []struct{ q, message, hint, at string }{
				{"SELECT 1 WHERE " + f + "('[]') IS NULL", "set-returning functions are not allowed in WHERE", "", f},
				{"SELECT CASE WHEN true THEN " + f + "('[]') ELSE NULL END", "set-returning functions are not allowed in CASE", hint, f},
				{"SELECT coalesce(" + f + "('[]'),NULL)", "set-returning functions are not allowed in COALESCE", hint, f},
				{"SELECT count(" + f + "('[]'))", "aggregate function calls cannot contain set-returning function calls", hint, f},
				{"SELECT * FROM " + f + "(json_array_elements('[[1]]'))", "set-returning functions must appear at top level of FROM", "", "json_array_elements('[[1]]')"},
			} {
				d := fixture.ControlsReferenceDiagnostic("0A000", tc.message)
				d.Hint = tc.hint
				d.Position = fixture.Position(len([]rune(tc.q[:strings.Index(tc.q, tc.at)])) + 1)
				jsonElementsError(t, p, b, c, tc.q, d, true)
			}
			q := "SELECT * FROM " + f + "('[]') AS j(v json)"
			d := fixture.ControlsReferenceDiagnostic("42601", `a column definition list is only allowed for functions returning "record"`)
			d.Position = fixture.Position(strings.Index(q, "v json") + 1)
			jsonElementsError(t, p, b, c, q, d, true)
		}
	})
}
