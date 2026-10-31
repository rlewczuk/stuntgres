package executor_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestBitwiseConstantNullPlanning(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	integer, _ := view.LookupType(sqltype.Int4)
	text, _ := view.LookupType(sqltype.Text)
	coercionPlan, err := sqltype.PlanCoercion(text.Usage(), integer.Usage(), sqltype.Explicit)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := sqltype.BindBitwise(sqltype.BitwiseAnd, integer, integer)
	if err != nil {
		t.Fatal(err)
	}
	rowInfo := expr.Info{Usage: text.Usage(), Property: expr.Properties{RowDependent: true}}
	column := plan.ColumnExpr{Info: rowInfo, Scope: 1, Frame: 1, Slot: 0}
	conversion := plan.CoercionExpr{Info: expr.Info{Usage: integer.Usage(), Property: rowInfo.Property}, Child: column, Plan: coercionPlan, Mode: sqltype.Explicit}
	null := plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: sqltype.Null(integer)}
	info := expr.Info{Usage: integer.Usage(), Pos: parser.Position{Offset: 7}, Property: expr.Properties{RowDependent: true, ParameterDependent: true, StageDependent: true}}
	bad, err := text.Input(context.Background(), "bad", -1)
	if err != nil {
		t.Fatal(err)
	}
	ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1, Row: []sqltype.Value{bad}}}
	for _, simplify := range []func(*exec.Context, expr.Expr, bool) (expr.Expr, bool, error){exec.PreflightCoercionsWithPolicy, exec.SimplifyConditional} {
		for _, reverse := range []bool{false, true} {
			root := plan.BitwiseExpr{Info: info, Left: null, Right: conversion, Provider: provider, Operator: sqltype.BitwiseAnd}
			if reverse {
				root.Left, root.Right = root.Right, root.Left
			}
			original := root
			for _, defaults := range []bool{false, true} {
				planned, changed, err := simplify(ctx, root, defaults)
				if err != nil || !changed {
					t.Fatal(planned, changed, err)
				}
				literal, ok := planned.(plan.LiteralExpr)
				if !ok || !reflect.DeepEqual(literal.Info, info) || literal.Value.Type() != integer || !literal.Value.IsNull() {
					t.Fatal("lost analyzed metadata or NULL type", planned)
				}
				if !reflect.DeepEqual(root, original) {
					t.Fatal("analysis tree mutated")
				}
				if _, err := root.Eval(ctx); err == nil {
					t.Fatal("runtime NULL incorrectly skipped bad row conversion")
				}
				if value, err := planned.Eval(ctx); err != nil || !value.IsNull() {
					t.Fatal(value, err)
				}
				identity, err := sqltype.PlanCoercion(integer.Usage(), integer.Usage(), sqltype.Explicit)
				if err != nil {
					t.Fatal(err)
				}
				wrapper := plan.CoercionExpr{Info: info, Child: root, Plan: identity, Mode: sqltype.Explicit}
				nested, changed, err := simplify(ctx, wrapper, defaults)
				if err != nil || !changed || !reflect.DeepEqual(nested.ResultUsage(), info.Usage) || nested.Properties() != info.Property || nested.SourcePosition() != info.Pos {
					t.Fatal("nested coercion lost metadata", nested, changed, err)
				}
				if value, err := nested.Eval(ctx); err != nil || !value.IsNull() {
					t.Fatal(value, err)
				}
				canceled, cancel := context.WithCancel(ctx.Context)
				cancel()
				cancelCtx := *ctx
				cancelCtx.Context = canceled
				if _, _, err := simplify(&cancelCtx, root, defaults); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				if _, err := planned.Eval(&cancelCtx); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			}
		}
		// Imported NULL parameters may become physical literals during cast
		// preflight, but their conservative parameter ownership is retained.
		parameter := plan.ParameterExpr{Info: expr.Info{Usage: text.Usage(), Property: expr.Properties{ParameterDependent: true}}, Number: 1}
		parameterCast := plan.CoercionExpr{Info: expr.Info{Usage: integer.Usage(), Property: parameter.Property}, Child: parameter, Plan: coercionPlan, Mode: sqltype.Explicit}
		root := plan.BitwiseExpr{Info: info, Left: parameterCast, Right: conversion, Provider: provider, Operator: sqltype.BitwiseAnd}
		valid, err := text.Input(ctx.Context, "3", -1)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range []sqltype.Value{sqltype.Null(text), valid} {
			ctx.Parameters = []sqltype.Value{value}
			planned, _, err := simplify(ctx, root, false)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := planned.(plan.BitwiseExpr); !ok {
				t.Fatal("parameter used as SQL constant", planned)
			}
			if _, err := planned.Eval(ctx); err == nil {
				t.Fatal("parameter skipped row conversion")
			}
		}
		// An immutable bad conversion still fails during child planning, even
		// though a constant NULL makes the operator itself unnecessary.
		constantBad := conversion
		constantBad.Property = expr.Properties{}
		constantBad.Child = plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: bad}
		root.Left, root.Right = null, constantBad
		if _, _, err := simplify(ctx, root, false); err == nil {
			t.Fatal("immutable bad conversion hidden")
		}
	}
}
