package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDeparseConstraintsDiagnostics(t *testing.T) {
	const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	for _, tc := range []struct{ q, code, message, position, hint string }{
		{"SELECT 'x'::pg_node_tree", "0A000", "cannot accept a value of type pg_node_tree", "8", ""},
		{"SELECT pg_get_expr('x',0)", "0A000", "cannot accept a value of type pg_node_tree", "20", ""},
		{"SELECT pg_get_expr('x'::text,0)", "42883", "function pg_get_expr(text, integer) does not exist", "8", hint},
		{"SELECT pg_get_constraintdef()", "42883", "function pg_get_constraintdef() does not exist", "8", hint},
		{"SELECT pg_get_constraintdef(true)", "42883", "function pg_get_constraintdef(boolean) does not exist", "8", hint},
		{"SELECT pg_get_constraintdef(0,1)", "42883", "function pg_get_constraintdef(integer, integer) does not exist", "8", hint},
		{"SELECT pg_get_constraintdef(0,false,false)", "42883", "function pg_get_constraintdef(integer, boolean, boolean) does not exist", "8", hint},
		{"SELECT pg_get_expr(NULL)", "42883", "function pg_get_expr(unknown) does not exist", "8", hint},
		{"SELECT pg_get_expr(NULL,true)", "42883", "function pg_get_expr(unknown, boolean) does not exist", "8", hint},
		{"SELECT pg_get_expr(NULL,0,1)", "42883", "function pg_get_expr(unknown, integer, integer) does not exist", "8", hint},
		{"SELECT pg_get_constrantdef(0)", "42883", "function pg_get_constrantdef(integer) does not exist", "8", hint},
		{`SELECT "PG_GET_CONSTRAINTDEF"(0)`, "42883", "function PG_GET_CONSTRAINTDEF(integer) does not exist", "8", hint},
		{"SELECT missing_schema.pg_get_constraintdef(0)", "3F000", `schema "missing_schema" does not exist`, "8", ""},
		{"SELECT /*é*/ pg_get_constraintdef(true)", "42883", "function pg_get_constraintdef(boolean) does not exist", "14", hint},
		// Source-derived signature diagnostics; root's permanent reference run
		// must independently establish these additional arities.
		{"SELECT pg_get_expr()", "42883", "function pg_get_expr() does not exist", "8", hint},
		{"SELECT pg_get_expr(NULL,0,false,false)", "42883", "function pg_get_expr(unknown, integer, boolean, boolean) does not exist", "8", hint},
	} {
		t.Run(tc.q, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprint(prepared), func(t *testing.T) {
					fixture.CheckBackends(t, func(t *testing.T, run func(fixture.Step)) {
						s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position, Hint: tc.hint})
						s.SelectRows = true
						s.Prepared = prepared
						if prepared {
							s.ErrorStage = "prepare"
						} else {
							s.ErrorStage = "execute"
						}
						run(s)
					})
				})
			}
		})
	}
}

func TestReferenceDeparseConstraintsPreparedInput(t *testing.T) {
	fixture.CheckBackends(t, func(t *testing.T, run func(fixture.Step)) {
		run(fixture.Prepared(fixture.Select("SELECT pg_get_expr($1,$2,$3) AS d", []string{"d"}, []string{"TEXT"}, [][]any{{nil}}, nil, 0, false)))
		// Both engines preserve the integrated parameter diagnostic contract:
		// nonempty input is redacted, while empty input remains empty.
		for _, tc := range []struct {
			args                   []any
			code, message, context string
		}{
			{[]any{"x", 0, false}, "0A000", "cannot accept a value of type pg_node_tree", `unnamed portal parameter $1 = '...'`},
			{[]any{"", 0, false}, "0A000", "cannot accept a value of type pg_node_tree", `unnamed portal parameter $1 = ''`},
			{[]any{nil, "bad", false}, "22P02", `invalid input syntax for type oid: "bad"`, `unnamed portal parameter $2 = '...'`},
			{[]any{nil, 0, "bad"}, "22P02", `invalid input syntax for type boolean: "bad"`, `unnamed portal parameter $3 = '...'`},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("SELECT pg_get_expr($1,$2,$3)", fixture.Diagnostic{Code: tc.code, Message: tc.message, Context: tc.context}, tc.args...)
				s.SelectRows = true
				s.Prepared = prepared
				s.ErrorStage = "execute"
				run(s)
			}
		}
	})
}
