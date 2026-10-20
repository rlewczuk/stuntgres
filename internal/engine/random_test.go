package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Construct raw calls through existing AST APIs, so baseline binder failures are
// observable independently of lexer support and of any new provider API.
func TestRandomBinder(t *testing.T) {
	lit := func(n int64) parser.Expr {
		return parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: n}}
	}
	for _, tc := range []struct {
		name string
		args []parser.Expr
		id   sqltype.ID
	}{
		{"random", nil, sqltype.Float8},
		{"random", []parser.Expr{lit(1), lit(2)}, sqltype.Int4},
		{"random", []parser.Expr{lit(2147483648), lit(2147483649)}, sqltype.Int8},
		{"random_normal", nil, sqltype.Float8},
		{"random_normal", []parser.Expr{lit(5)}, sqltype.Float8},
		{"random_normal", []parser.Expr{lit(5), lit(0)}, sqltype.Float8},
		{"setseed", []parser.Expr{lit(0)}, sqltype.ID(2278)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
			x, err := b.Bind(nullCountRaw(tc.name, tc.args...), analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			if err != nil {
				t.Fatal(err)
			}
			if x.ResultUsage().Type.ID() != tc.id || x.Properties().Volatility != expr.Volatile {
				t.Fatalf("usage/properties: %#v %#v", x.ResultUsage(), x.Properties())
			}
		})
	}
}

func TestRandomBinderAmbiguousParameters(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	_, err := b.Bind(nullCountRaw("random", parser.ParameterExpr{Number: 1}, parser.ParameterExpr{Number: 2}), analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "42725", Message: "function random(unknown, unknown) is not unique", Position: "8", Hint: "Could not choose a best candidate function. You might need to add explicit type casts."})
}
