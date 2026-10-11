package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

// Original public consumer routes run in fresh phases; the Context companions remain intact.
func TestReferenceDeparseConstraintsDiagnosticsSourceConsumerRoutes(t *testing.T) {
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
						s.NonContext = true
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
