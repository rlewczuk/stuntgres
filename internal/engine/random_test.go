package engine

import (
	"context"
	"testing"

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
			b := newExpressionBinder(context.Background(), New(), sqltype.InputEnvironment{}, nil, nil)
			x, err := b.bind(nullCountRaw(tc.name, tc.args...), expressionPolicy{context: projectionContext})
			if err != nil {
				t.Fatal(err)
			}
			if x.ResultUsage().Type.ID() != tc.id || x.Properties().Volatility != Volatile {
				t.Fatalf("usage/properties: %#v %#v", x.ResultUsage(), x.Properties())
			}
		})
	}
}

func TestRandomBinderAmbiguousParameters(t *testing.T) {
	b := newExpressionBinder(context.Background(), New(), sqltype.InputEnvironment{}, nil, nil)
	_, err := b.bind(nullCountRaw("random", parser.ParameterExpr{Number: 1}, parser.ParameterExpr{Number: 2}), expressionPolicy{context: projectionContext})
	nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "42725", Message: "function random(unknown, unknown) is not unique", Position: "8", Hint: "Could not choose a best candidate function. You might need to add explicit type casts."})
}

func TestRandomIntegerSampler(t *testing.T) {
	for _, tc := range []struct {
		lo, hi int64
		words  []uint64
		want   int64
	}{
		{7, 7, nil, 7},
		{-1, 1, []uint64{0, 1}, 0},
		{-1, 1, []uint64{2}, 1},
		{-1, 1, []uint64{3}, -1},
		{-9223372036854775808, 9223372036854775807, []uint64{0}, -9223372036854775808},
		{-9223372036854775808, 9223372036854775807, []uint64{^uint64(0)}, 9223372036854775807},
		{-2, 1, []uint64{3}, 1},
	} {
		i := 0
		v := randomInteger(tc.lo, tc.hi, func() uint64 {
			if i >= len(tc.words) {
				t.Fatal("unexpected draw")
			}
			w := tc.words[i]
			i++
			return w
		})
		if v != tc.want || i != len(tc.words) {
			t.Fatal(v, tc.want, i)
		}
	}
}

func TestRandomRuntimeNullEvaluatesSibling(t *testing.T) {
	b := newExpressionBinder(context.Background(), New(), sqltype.InputEnvironment{}, nil, nil)
	d := b.typ(sqltype.Float8)
	v, err := sqltype.Float(d, 1)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	first := nullCountWitness{expressionInfo: expressionInfo{usage: d.Usage()}, value: sqltype.Null(d), calls: &calls}
	last := nullCountWitness{expressionInfo: expressionInfo{usage: d.Usage()}, value: v, calls: &calls}
	x := randomExpression{expressionInfo: expressionInfo{usage: d.Usage()}, name: "random_normal", args: []BoundExpr{first, last}}
	result, err := x.Eval(&EvalContext{Context: context.Background()})
	if err != nil || !result.IsNull() || result.Type() != d || calls != 2 {
		t.Fatal(result, err, calls)
	}
}
