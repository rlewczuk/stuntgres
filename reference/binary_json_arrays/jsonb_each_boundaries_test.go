package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// Complete logical diagnostics are independently specified and checked on PostgreSQL 18.0.
func jsonbEachBoundaryDiagnostics(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, f string) {
	t.Helper()
	for _, tc := range []struct{ input, code, message, detail, context string }{
		{`{"a":"\u0000"}`, "22P05", "unsupported Unicode escape sequence", `\u0000 cannot be converted to text.`, `JSON data, line 1: {"a":"\u0000...`},
		{`{"a":"\ud800"}`, "22P02", "invalid input syntax for type json", "Unicode low surrogate must follow a high surrogate.", `JSON data, line 1: {"a":"\ud800"...`},
		{`{"a":1e999999}`, "22003", "value overflows numeric format", "", ""},
	} {
		for _, bound := range []bool{false, true} {
			q := "/*é*/ SELECT " + f + "('" + tc.input + "'::jsonb)"
			var args []any
			d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
			d.Detail, d.Context = tc.detail, tc.context
			if bound {
				q = "/*é*/ SELECT " + f + "($1)"
				args = []any{tc.input}
				if d.Context != "" {
					d.Context += "\n"
				}
				d.Context += "unnamed portal parameter $1 = '...'"
			} else {
				d.Position = fixture.Position(len([]rune("/*é*/ SELECT "+f+"(")) + 1)
			}
			for _, prepared := range []bool{false, true} {
				stage := "execute"
				if prepared && !bound {
					stage = "prepare"
				}
				jsonKeysError(t, p, b, c, q, prepared, d, stage, args...)
			}
		}
	}
	for _, decoration := range []string{"DISTINCT", "ORDER BY", "FILTER", "OVER"} {
		name := "pg_catalog." + f
		call := name + "('{}'::jsonb)"
		switch decoration {
		case "DISTINCT":
			call = name + "(DISTINCT '{}'::jsonb)"
		case "ORDER BY":
			call = name + "('{}'::jsonb ORDER BY 1)"
		case "FILTER":
			call += " FILTER (WHERE true)"
		case "OVER":
			call += " OVER ()"
		}
		message := decoration + " specified, but " + name + " is not an aggregate function"
		if decoration == "OVER" {
			message = "OVER specified, but " + name + " is not a window function nor an aggregate function"
		}
		q := "/*é*/ SELECT " + call
		d := fixture.ControlsReferenceDiagnostic("42809", message)
		d.Position = fixture.Position(len([]rune("/*é*/ SELECT ")) + 1)
		for _, prepared := range []bool{false, true} {
			stage := "execute"
			if prepared {
				stage = "prepare"
			}
			jsonKeysError(t, p, b, c, q, prepared, d, stage)
		}
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE jbeach_placement_"+f+"(j jsonb)", 0))
	for _, tc := range []struct{ q, code, message, at string }{
		{"CREATE TABLE jbeach_default_" + f + "(j text DEFAULT (" + f + "('{}')).key)", "0A000", "set-returning functions are not allowed in DEFAULT expressions", f + "("},
		{"ALTER TABLE jbeach_placement_" + f + " ADD CHECK (" + f + "(j) IS NULL)", "0A000", "set-returning functions are not allowed in check constraints", ""},
		{"CREATE INDEX ON jbeach_placement_" + f + " ((" + f + "(j)))", "0A000", "set-returning functions are not allowed in index expressions", f + "("},
		{"CREATE TABLE jbeach_generated_" + f + "(j jsonb,k text GENERATED ALWAYS AS ((" + f + "(j)).key) STORED)", "0A000", "set-returning functions are not allowed in column generation expressions", f + "("},
		{"SELECT * FROM " + f + "((jsonb_each('{\"a\":{}}')).value)", "0A000", "set-returning functions must appear at top level of FROM", "jsonb_each('{\"a\""},
		{"SELECT * FROM " + f + "('{}') AS j(k,v,extra)", "42P10", "table \"j\" has 2 columns available but 3 columns specified", ""},
		{"SELECT * FROM " + f + "('{}') AS j(k text,v jsonb)", "42601", "a column definition list is redundant for a function with OUT parameters", "k text"},
		{"SELECT * FROM " + f + "('{}') trailing garbage", "42601", "syntax error at or near \"trailing\"", "trailing"},
	} {
		t.Run(tc.q, func(t *testing.T) {
			d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
			if tc.at != "" {
				d.Position = fixture.Position(len([]rune(tc.q[:strings.LastIndex(tc.q, tc.at)])) + 1)
			}
			for _, prepared := range []bool{false, true} {
				stage := "execute"
				// PostgreSQL defers these utility-expression checks until execution.
				if prepared && strings.HasPrefix(tc.q, "SELECT ") {
					stage = "prepare"
				}
				jsonKeysError(t, p, b, c, tc.q, prepared, d, stage)
			}
		})
	}
}
