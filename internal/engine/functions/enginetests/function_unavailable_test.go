package enginetests_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestUnavailableFunctionIdentityBoundary(t *testing.T) {
	e := engine.New()
	for _, test := range []struct {
		expression string
		code       string
	}{
		{"pg_backend_pid()", "0A000"},
		{"pg_catalog.pg_export_snapshot()", "0A000"},
		{"pg_read_file('missing')", "0A000"},
		{"pg_cancel_backend(1)", "0A000"},
		{"pg_terminate_backend(1)", "0A000"},
		{"pg_terminate_backend(1,0)", "0A000"},
		{"pg_terminate_backend()", "42883"},
		{"pg_terminate_backend(true)", "42883"},
		{"pg_terminate_backend(1,true)", "42883"},
		{"pg_terminate_backend(1,0,0)", "42883"},
		{"byteacat('a'::bytea,'b'::bytea)", "0A000"},
		{"pg_backend_pid(1)", "42883"},
		{"pg_read_file(true)", "42883"},
		{"current_setting(true)", "42883"},
		{"byteacat(1,2)", "42883"},
		{"public.pg_backend_pid()", "42883"},
		{"\"PG_BACKEND_PID\"()", "42883"},
		{"routine_not_present(1)", "42883"},
		{"pgrowlocks('t')", "42883"},
	} {
		t.Run(test.expression, func(t *testing.T) {
			b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			raw := enginetest.Statement(t, "SELECT 0, "+test.expression).(*parser.Select).Targets[1]
			// Exercise the ordinary expression provider directly, not the separate
			// top-level setting statement route.
			call, ok := raw.(parser.FunctionExpr)
			if !ok {
				t.Fatalf("not an ordinary function expression: %T", raw)
			}
			node, err := b.BindFunctionProvider(call)
			var diagnostic *sqlstate.Error
			if node != nil || !errors.As(err, &diagnostic) || diagnostic.Code != test.code || diagnostic.Position != "11" {
				t.Fatalf("node=%T diagnostic=%v, want %s at 11", node, err, test.code)
			}
			if test.code == "42883" && diagnostic.Hint != "No function matches the given name and argument types. You might need to add explicit type casts." {
				t.Fatal("missing signature diagnostic", diagnostic)
			}
		})
	}
}
