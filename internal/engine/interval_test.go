package engine

import (
	"context"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestIntervalImmutableConstantCapture(t *testing.T) {
	e := New()
	ctx := context.Background()
	d, _ := e.CatalogRead().LookupType(sqltype.Interval)
	for _, typed := range []bool{false, true} {
		raw := parser.Expr(parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "-1 2:03:04", Pos: parser.Position{Offset: 7, Character: 8}}})
		if typed {
			raw = parser.TypedLiteralExpr{TypeName: parser.TypeName{Name: "interval"}, Text: "-1 2:03:04", Pos: parser.Position{Offset: 7, Character: 8}}
		}
		policy := expressionPolicy{context: assignmentContext, expected: d.Usage(), mode: sqltype.Assignment}
		b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{IntervalStyle: sqltype.IntervalPostgres}, nil)
		first, err := b.bind(raw, policy)
		if err != nil {
			t.Fatal(err)
		}
		description := StatementDescription{temporals: b.prepared.Temporals}
		b = newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{IntervalStyle: sqltype.IntervalSQLStandard}, &description)
		rebound, err := b.bind(raw, policy)
		if err != nil {
			t.Fatal(err)
		}
		a, err := first.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
		if err != nil {
			t.Fatal(err)
		}
		v, err := rebound.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
		if err != nil {
			t.Fatal(err)
		}
		if !sqltype.SameDatum(a, v) {
			t.Fatal("prepared input was reparsed")
		}
		_, days, us, err := v.Interval()
		if err != nil || days != -1 || us != 7384000000 {
			t.Fatal(days, us, err)
		}
	}
}
