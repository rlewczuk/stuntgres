package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestJSONBSetDecorationDiagnostics(t *testing.T) {
	for _, name := range []string{"jsonb_set", "jsonb_set_lax"} {
		for _, tc := range []struct{ prefix, inner, outer, decoration, kind string }{
			{prefix: "DISTINCT ", decoration: "DISTINCT", kind: "not an aggregate function"},
			{inner: " ORDER BY 1", decoration: "ORDER BY", kind: "not an aggregate function"},
			{outer: " FILTER (WHERE true)", decoration: "FILTER", kind: "not an aggregate function"},
			{outer: " OVER ()", decoration: "OVER", kind: "not a window function nor an aggregate function"},
		} {
			q := "/*é*/ SELECT " + name + "(" + tc.prefix + "'{}','{a}','2'" + tc.inner + ")" + tc.outer
			t.Run(q, func(t *testing.T) {
				b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
				raw := statement(t, q).(*parser.Select).Targets[0].(parser.FunctionExpr)
				_, err := b.bindFunctionProvider(raw)
				var got *sqlstate.Error
				want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: tc.decoration + " specified, but " + name + " is " + tc.kind, Position: "14"}
				if !errors.As(err, &got) || *got != want {
					t.Fatalf("scalar modifier diagnostic=%+v want=%+v", got, want)
				}
			})
		}
	}
}
