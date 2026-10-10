package engine

import (
	"context"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestMaterializedViewSnapshotLosslessCoercions(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	input, _ := sessionPresentation(op.session)
	input.ExactSnapshot = true
	for _, id := range []sqltype.ID{sqltype.Timestamp, sqltype.Time, sqltype.Timestamptz} {
		b := newExpressionBinder(context.Background(), e, input, op, nil)
		u, _, err := b.typ(id).ResolveModifiers(3)
		if err != nil {
			t.Fatal(err)
		}
		text := "2024-01-02 12:34:56.123789"
		if id == sqltype.Time {
			text = "12:34:56.123789"
		}
		if id == sqltype.Timestamptz {
			text += "+00"
		}
		if _, err := b.inputConstant(text, u, parser.Position{}); err == nil {
			t.Fatal("bind-time temporal input erased precision", id)
		}
		if len(b.temporals) != 0 {
			t.Fatal("failed input installed temporal capture")
		}
	}
	b := newExpressionBinder(context.Background(), e, input, op, nil)
	u, _, err := b.typ(sqltype.Numeric).ResolveModifiers(5, 2)
	if err != nil {
		t.Fatal(err)
	}
	v, err := u.Type.Input(context.Background(), "12.345", -1)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := sqltype.PlanCoercion(u.Type.Usage(), u, sqltype.Explicit)
	if err != nil {
		t.Fatal(err)
	}
	literal := literalExpression{expressionInfo{usage: u.Type.Usage()}, v}
	inner := coercionExpression{expressionInfo{usage: u}, literal, plan, sqltype.Explicit}
	// The explicit node already has the destination usage. A final assignment
	// wrapper is neither required nor sufficient to validate its child.
	if _, err := inner.Eval(&EvalContext{Context: context.Background(), Input: input}); err == nil {
		t.Fatal("matching explicit usage lost datum")
	}
	wide, err := sqltype.PlanCoercion(u, u.Type.Usage(), sqltype.Explicit)
	if err != nil {
		t.Fatal(err)
	}
	outer := coercionExpression{expressionInfo{usage: u.Type.Usage()}, inner, wide, sqltype.Explicit}
	if _, err := outer.Eval(&EvalContext{Context: context.Background(), Input: input}); err == nil {
		t.Fatal("outer widening hid inner loss")
	}
	// Ordinary SQL retains its intentional rounding semantics.
	if _, err := outer.Eval(&EvalContext{Context: context.Background()}); err != nil {
		t.Fatal("ordinary coercion changed", err)
	}
}
