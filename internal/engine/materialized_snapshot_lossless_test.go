package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
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
		if len(b.prepared.Temporals) != 0 {
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
	coercionPlan, err := sqltype.PlanCoercion(u.Type.Usage(), u, sqltype.Explicit)
	if err != nil {
		t.Fatal(err)
	}
	literal := plan.LiteralExpr{Info: expr.Info{Usage: u.Type.Usage()}, Value: v}
	inner := plan.CoercionExpr{Info: expr.Info{Usage: u}, Child: literal, Plan: coercionPlan, Mode: sqltype.Explicit}
	// The explicit node already has the destination usage. A final assignment
	// wrapper is neither required nor sufficient to validate its child.
	if _, err := inner.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Input: input}}); err == nil {
		t.Fatal("matching explicit usage lost datum")
	}
	wide, err := sqltype.PlanCoercion(u, u.Type.Usage(), sqltype.Explicit)
	if err != nil {
		t.Fatal(err)
	}
	outer := plan.CoercionExpr{Info: expr.Info{Usage: u.Type.Usage()}, Child: inner, Plan: wide, Mode: sqltype.Explicit}
	if _, err := outer.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Input: input}}); err == nil {
		t.Fatal("outer widening hid inner loss")
	}
	// Ordinary SQL retains its intentional rounding semantics.
	if _, err := outer.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}); err != nil {
		t.Fatal("ordinary coercion changed", err)
	}
	// The routine container codec emits a closed concatenation tree. Admission
	// must recurse through both operands rather than accept arbitrary SQL below
	// a recognized operator.
	for _, sample := range []struct {
		sql   string
		valid bool
	}{
		{`SELECT '(' || ('pg_catalog.abs(integer)'::regprocedure::oid::text) || ',integer)'`, true},
		{`SELECT 'prefix' || lower('suffix')`, false},
		{`SELECT 'prefix' || current_user`, false},
		{`SELECT 'prefix' || missing_column`, false},
		{`SELECT 'prefix' || (1 + 2)::text`, false},
	} {
		statements, err := parser.Parse(sample.sql)
		if err != nil {
			t.Fatal(sample.sql, err)
		}
		raw := statements[0].(*parser.Select).Targets[0]
		if err := materializedSnapshotScalar(raw); (err == nil) != sample.valid {
			t.Fatal("snapshot concatenation boundary", sample.sql, err)
		}
	}
}
