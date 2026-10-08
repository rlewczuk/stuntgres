package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestBitwiseDefaultValidationAndRetag(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE d(i INTEGER DEFAULT (1 & 3),s SMALLINT DEFAULT (1::smallint << 15),n SMALLINT DEFAULT (~NULL::smallint))")
	view := e.CatalogRead()
	ref := roleTableRef(t, e, "d")
	old := view.state.descriptors[ref.Address].(*relationDescriptor)
	if err := (relationProvider{}).Validate(view, old); err != nil {
		t.Fatal(err)
	}
	root := old.defaults[1].expression.(bitwiseExpression)
	for _, mutate := range []func(bitwiseExpression) bitwiseExpression{
		func(n bitwiseExpression) bitwiseExpression { n.left = nil; return n },
		func(n bitwiseExpression) bitwiseExpression { n.right = nil; return n },
		func(n bitwiseExpression) bitwiseExpression { n.operator = sqltype.BitwiseOperator(255); return n },
		func(n bitwiseExpression) bitwiseExpression { n.operator = sqltype.BitwiseOr; return n },
		func(n bitwiseExpression) bitwiseExpression { n.provider = sqltype.Bitwise{}; return n },
		func(n bitwiseExpression) bitwiseExpression { n.right = old.defaults[2].expression; return n },
		func(n bitwiseExpression) bitwiseExpression {
			l := n.left.(literalExpression)
			l.properties.RowDependent = true
			n.left = l
			return n
		},
		func(n bitwiseExpression) bitwiseExpression { n.right = literalExpression{}; return n },
		func(n bitwiseExpression) bitwiseExpression {
			foreign := New()
			typ, _ := foreign.CatalogRead().LookupType(sqltype.Int4)
			value, _ := sqltype.Integer(typ, 1)
			n.left = literalExpression{expressionInfo: expressionInfo{usage: typ.Usage()}, value: value}
			return n
		},
	} {
		if _, err := defaultSQL(context.Background(), view, mutate(root)); err == nil {
			t.Fatal("invalid bitwise default accepted")
		}
	}
	shift := old.defaults[2].expression.(bitwiseExpression)
	shift.right = old.defaults[3].expression
	if _, err := defaultSQL(context.Background(), view, shift); err == nil {
		t.Fatal("SMALLINT bound shift count accepted")
	}
	ctx := &EvalContext{Context: context.Background()}
	ordinary, changed, err := preflightCoercions(ctx, root)
	if err != nil || changed {
		t.Fatal(ordinary, changed, err)
	}
	if _, ok := ordinary.(bitwiseExpression); !ok {
		t.Fatal("ordinary node folded")
	}
	folded, changed, err := preflightCoercionsWithPolicy(ctx, root, true)
	if err != nil || !changed {
		t.Fatal(folded, changed, err)
	}
	v, err := folded.Eval(ctx)
	n, _ := v.Integer()
	if err != nil || n != 1 {
		t.Fatal(v, err)
	}
	if _, ok := old.defaults[1].expression.(bitwiseExpression); !ok {
		t.Fatal("catalog mutated")
	}
	replacement := New()
	retag := func(d *sqltype.Descriptor) *sqltype.Descriptor {
		x, _ := replacement.CatalogRead().LookupType(d.ID())
		return x
	}
	for _, attribute := range []int32{1, 2, 3} {
		original := old.defaults[attribute].expression
		rebound, changed, err := retagBoundTypes(original, retag)
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
		if _, err := defaultSQL(context.Background(), replacement.CatalogRead(), rebound); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBitwiseScopedCastDefaultSerializationAndRetag(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE d(w BIGINT DEFAULT CAST(32767::smallint << 1::smallint AS bigint),n INTEGER DEFAULT CAST(~NULL::smallint AS integer))")
	view := e.CatalogRead()
	ref := roleTableRef(t, e, "d")
	table := view.state.descriptors[ref.Address].(*relationDescriptor)
	replacement := New()
	retag := func(d *sqltype.Descriptor) *sqltype.Descriptor {
		typ, _ := replacement.CatalogRead().LookupType(d.ID())
		return typ
	}
	ctx := &EvalContext{Context: context.Background()}
	for _, attribute := range []int32{1, 2} {
		original := table.defaults[attribute].expression
		rebound, changed, err := retagBoundTypes(original, retag)
		if err != nil || !changed {
			t.Fatal(rebound, changed, err)
		}
		for _, node := range []BoundExpr{original, rebound} {
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
		sql, err := defaultSQL(context.Background(), replacement.CatalogRead(), rebound)
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
		restored := replacement.CatalogRead().state.descriptors[restoredRef.Address].(*relationDescriptor)
		value, err := restored.defaults[1].expression.Eval(ctx)
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
	plan, err := sqltype.PlanCoercion(text.Usage(), integer.Usage(), sqltype.Explicit)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := sqltype.BindBitwise(sqltype.BitwiseAnd, integer, integer)
	if err != nil {
		t.Fatal(err)
	}
	rowInfo := expressionInfo{usage: text.Usage(), properties: ExprProperties{RowDependent: true}}
	column := columnExpression{expressionInfo: rowInfo, scope: 1, frame: 1, slot: 0}
	conversion := coercionExpression{expressionInfo: expressionInfo{usage: integer.Usage(), properties: rowInfo.properties}, child: column, plan: plan, mode: sqltype.Explicit}
	null := literalExpression{expressionInfo: expressionInfo{usage: integer.Usage()}, value: sqltype.Null(integer)}
	info := expressionInfo{usage: integer.Usage(), pos: parser.Position{Offset: 7}, properties: ExprProperties{RowDependent: true, ParameterDependent: true, StageDependent: true}}
	bad, err := text.Input(context.Background(), "bad", -1)
	if err != nil {
		t.Fatal(err)
	}
	ctx := &EvalContext{Context: context.Background(), Scope: 1, Frame: 1, Row: []sqltype.Value{bad}}
	for _, simplify := range []func(*EvalContext, BoundExpr, bool) (BoundExpr, bool, error){preflightCoercionsWithPolicy, simplifyConditional} {
		for _, reverse := range []bool{false, true} {
			root := bitwiseExpression{expressionInfo: info, left: null, right: conversion, provider: provider, operator: sqltype.BitwiseAnd}
			if reverse {
				root.left, root.right = root.right, root.left
			}
			original := root
			for _, defaults := range []bool{false, true} {
				planned, changed, err := simplify(ctx, root, defaults)
				if err != nil || !changed {
					t.Fatal(planned, changed, err)
				}
				literal, ok := planned.(literalExpression)
				if !ok || !reflect.DeepEqual(literal.expressionInfo, info) || literal.value.Type() != integer || !literal.value.IsNull() {
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
				wrapper := coercionExpression{expressionInfo: info, child: root, plan: identity, mode: sqltype.Explicit}
				nested, changed, err := simplify(ctx, wrapper, defaults)
				if err != nil || !changed || !reflect.DeepEqual(nested.ResultUsage(), info.usage) || nested.Properties() != info.properties || nested.SourcePosition() != info.pos {
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
		parameter := parameterExpression{expressionInfo: expressionInfo{usage: text.Usage(), properties: ExprProperties{ParameterDependent: true}}, number: 1}
		parameterCast := coercionExpression{expressionInfo: expressionInfo{usage: integer.Usage(), properties: parameter.properties}, child: parameter, plan: plan, mode: sqltype.Explicit}
		root := bitwiseExpression{expressionInfo: info, left: parameterCast, right: conversion, provider: provider, operator: sqltype.BitwiseAnd}
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
			if _, ok := planned.(bitwiseExpression); !ok {
				t.Fatal("parameter used as SQL constant", planned)
			}
			if _, err := planned.Eval(ctx); err == nil {
				t.Fatal("parameter skipped row conversion")
			}
		}
		// An immutable bad conversion still fails during child planning, even
		// though a constant NULL makes the operator itself unnecessary.
		constantBad := conversion
		constantBad.properties = ExprProperties{}
		constantBad.child = literalExpression{expressionInfo: expressionInfo{usage: text.Usage()}, value: bad}
		root.left, root.right = null, constantBad
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
	table := view.state.descriptors[ref.Address].(*relationDescriptor)
	original := table.defaults[1].expression
	before, err := defaultSQL(context.Background(), view, original)
	if err != nil {
		t.Fatal(err)
	}
	ctx := &EvalContext{Context: context.Background()}
	planned, changed, err := preflightCoercionsWithPolicy(ctx, original, true)
	if err != nil || !changed {
		t.Fatal(planned, changed, err)
	}
	if value, err := planned.Eval(ctx); err != nil || !value.IsNull() || value.Type() != original.ResultUsage().Type {
		t.Fatal(value, err)
	}
	if _, ok := table.defaults[1].expression.(bitwiseExpression); !ok {
		t.Fatal("stored default replaced")
	}
	after, err := defaultSQL(context.Background(), view, table.defaults[1].expression)
	if err != nil || before != after {
		t.Fatal(before, after, err)
	}
	if err := (relationProvider{}).Validate(view, table); err != nil {
		t.Fatal(err)
	}
}
