package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestBitwiseDefaultValidationAndRetag(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE d(i INTEGER DEFAULT (1 & 3),s SMALLINT DEFAULT (1::smallint << 15),n SMALLINT DEFAULT (~NULL::smallint))")
	view := e.CatalogRead()
	ref := roleTableRef(t, e, "d")
	old := view.MustRelation(ref.Address)
	if err := (relationProvider{}).Validate(view, old); err != nil {
		t.Fatal(err)
	}
	root := old.ColumnMeta.Defaults[1].Expression.(plan.BitwiseExpr)
	for _, mutate := range []func(plan.BitwiseExpr) plan.BitwiseExpr{
		func(n plan.BitwiseExpr) plan.BitwiseExpr { n.Left = nil; return n },
		func(n plan.BitwiseExpr) plan.BitwiseExpr { n.Right = nil; return n },
		func(n plan.BitwiseExpr) plan.BitwiseExpr { n.Operator = sqltype.BitwiseOperator(255); return n },
		func(n plan.BitwiseExpr) plan.BitwiseExpr { n.Operator = sqltype.BitwiseOr; return n },
		func(n plan.BitwiseExpr) plan.BitwiseExpr { n.Provider = sqltype.Bitwise{}; return n },
		func(n plan.BitwiseExpr) plan.BitwiseExpr { n.Right = old.ColumnMeta.Defaults[2].Expression; return n },
		func(n plan.BitwiseExpr) plan.BitwiseExpr {
			l := n.Left.(plan.LiteralExpr)
			l.Property.RowDependent = true
			n.Left = l
			return n
		},
		func(n plan.BitwiseExpr) plan.BitwiseExpr { n.Right = plan.LiteralExpr{}; return n },
		func(n plan.BitwiseExpr) plan.BitwiseExpr {
			foreign := New()
			typ, _ := foreign.CatalogRead().LookupType(sqltype.Int4)
			value, _ := sqltype.Integer(typ, 1)
			n.Left = plan.LiteralExpr{Info: expr.Info{Usage: typ.Usage()}, Value: value}
			return n
		},
	} {
		if _, err := stored.DefaultSQL(context.Background(), view, mutate(root)); err == nil {
			t.Fatal("invalid bitwise default accepted")
		}
	}
	shift := old.ColumnMeta.Defaults[2].Expression.(plan.BitwiseExpr)
	shift.Right = old.ColumnMeta.Defaults[3].Expression
	if _, err := stored.DefaultSQL(context.Background(), view, shift); err == nil {
		t.Fatal("SMALLINT bound shift count accepted")
	}
	ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
	ordinary, changed, err := exec.PreflightCoercions(ctx, root)
	if err != nil || changed {
		t.Fatal(ordinary, changed, err)
	}
	if _, ok := ordinary.(plan.BitwiseExpr); !ok {
		t.Fatal("ordinary node folded")
	}
	folded, changed, err := exec.PreflightCoercionsWithPolicy(ctx, root, true)
	if err != nil || !changed {
		t.Fatal(folded, changed, err)
	}
	v, err := folded.Eval(ctx)
	n, _ := v.Integer()
	if err != nil || n != 1 {
		t.Fatal(v, err)
	}
	if _, ok := old.ColumnMeta.Defaults[1].Expression.(plan.BitwiseExpr); !ok {
		t.Fatal("catalog mutated")
	}
	replacement := New()
	retag := func(d *sqltype.Descriptor) *sqltype.Descriptor {
		x, _ := replacement.CatalogRead().LookupType(d.ID())
		return x
	}
	for _, attribute := range []int32{1, 2, 3} {
		original := old.ColumnMeta.Defaults[attribute].Expression
		rebound, changed, err := stored.RetagTypes(replacement.CatalogRead(), original, retag)
		if err != nil || !changed || rebound.ResultUsage().Type == original.ResultUsage().Type {
			t.Fatal(rebound, changed, err)
		}
		a, err := original.Eval(ctx)
		if err != nil {
			t.Fatal(err)
		}
		b, err := rebound.Eval(ctx)
		if err != nil || a.IsNull() != b.IsNull() {
			t.Fatal(a, b, err)
		}
		av, _ := a.Integer()
		bv, _ := b.Integer()
		if av != bv {
			t.Fatal(av, bv)
		}
		if _, err := stored.DefaultSQL(context.Background(), replacement.CatalogRead(), rebound); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBitwiseScopedCastDefaultSerializationAndRetag(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE d(w BIGINT DEFAULT CAST(32767::smallint << 1::smallint AS bigint),n INTEGER DEFAULT CAST(~NULL::smallint AS integer))")
	view := e.CatalogRead()
	ref := roleTableRef(t, e, "d")
	table := view.MustRelation(ref.Address)
	replacement := New()
	retag := func(d *sqltype.Descriptor) *sqltype.Descriptor {
		typ, _ := replacement.CatalogRead().LookupType(d.ID())
		return typ
	}
	ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
	for _, attribute := range []int32{1, 2} {
		original := table.ColumnMeta.Defaults[attribute].Expression
		rebound, changed, err := stored.RetagTypes(replacement.CatalogRead(), original, retag)
		if err != nil || !changed {
			t.Fatal(rebound, changed, err)
		}
		for _, node := range []expr.Expr{original, rebound} {
			value, err := node.Eval(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if attribute == 2 {
				if !value.IsNull() || value.Type().ID() != sqltype.Int4 {
					t.Fatal("lost typed NULL", value)
				}
			} else if n, err := value.Integer(); err != nil || n != -2 || value.Type().ID() != sqltype.Int8 {
				t.Fatal("lost narrow truncation before widening", value)
			}
		}
		sql, err := stored.DefaultSQL(context.Background(), replacement.CatalogRead(), rebound)
		if err != nil {
			t.Fatal(err)
		}
		name, target := "w", "BIGINT"
		if attribute == 2 {
			name, target = "n", "INTEGER"
		}
		run(t, replacement, "CREATE TABLE "+name+"(v "+target+" DEFAULT "+sql+")")
		run(t, replacement, "INSERT INTO "+name+" DEFAULT VALUES")
		restoredRef := roleTableRef(t, replacement, name)
		restored := replacement.CatalogRead().MustRelation(restoredRef.Address)
		value, err := restored.ColumnMeta.Defaults[1].Expression.Eval(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if attribute == 2 {
			if !value.IsNull() {
				t.Fatal("NULL changed on replay", value)
			}
		} else if n, err := value.Integer(); err != nil || n != -2 {
			t.Fatal("width changed on replay", value)
		}
	}
}

func TestBitwiseConstantNullPlanning(t *testing.T) {
	e := New()
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

func TestBitwiseConstantNullDefaultRemainsAnalyzed(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE null_default(v INTEGER DEFAULT (NULL::integer & 1))")
	view := e.CatalogRead()
	ref := roleTableRef(t, e, "null_default")
	table := view.MustRelation(ref.Address)
	original := table.ColumnMeta.Defaults[1].Expression
	before, err := stored.DefaultSQL(context.Background(), view, original)
	if err != nil {
		t.Fatal(err)
	}
	ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
	planned, changed, err := exec.PreflightCoercionsWithPolicy(ctx, original, true)
	if err != nil || !changed {
		t.Fatal(planned, changed, err)
	}
	if value, err := planned.Eval(ctx); err != nil || !value.IsNull() || value.Type() != original.ResultUsage().Type {
		t.Fatal(value, err)
	}
	if _, ok := table.ColumnMeta.Defaults[1].Expression.(plan.BitwiseExpr); !ok {
		t.Fatal("stored default replaced")
	}
	after, err := stored.DefaultSQL(context.Background(), view, table.ColumnMeta.Defaults[1].Expression)
	if err != nil || before != after {
		t.Fatal(before, after, err)
	}
	if err := (relationProvider{}).Validate(view, table); err != nil {
		t.Fatal(err)
	}
}
