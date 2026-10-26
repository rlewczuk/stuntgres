package enginetests_test

import (
	"context"
	"testing"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestPreparedFoldTypedOwnershipScratch(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TYPE fold_pair AS (i integer,n text)")
	view := e.CatalogRead()
	public := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	address := view.NameAddress(ids.NameKey{ClassID: ids.TypeClassID, Namespace: public, Name: "fold_pair"})
	descriptor := view.MustType(address).SQLType
	integerType, _ := view.LookupType(sqltype.Int4)
	textType, _ := view.LookupType(sqltype.Text)
	integer, err := sqltype.Integer(integerType, 7)
	if err != nil { t.Fatal(err) }
	compound, err := sqltype.CompositeValue(descriptor, []sqltype.Value{integer, sqltype.String(textType, "owned")})
	if err != nil { t.Fatal(err) }
	// The type-owned constructor detaches caller slices once; a saved Value
	// cannot expose mutable constructor or provider scratch through accessors.
	fields := []sqltype.Value{compound.CompositeField(0), compound.CompositeField(1)}
	owned, err := sqltype.CompositeValue(compound.Type(), fields)
	if err != nil {
		t.Fatal(err)
	}
	capture := func(value sqltype.Value) *exec.FoldCaptureCandidate {
		t.Helper()
		usage := value.Type().Usage()
		conversion, err := sqltype.PlanCoercion(usage, usage, sqltype.Explicit)
		if err != nil {
			t.Fatal(err)
		}
		node := plan.CoercionExpr{Info: expr.Info{Usage: usage}, Child: plan.LiteralExpr{Info: expr.Info{Usage: usage}, Value: value}, Plan: conversion}
		group := &exec.FoldCaptureCandidate{}
		ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: e.CatalogRead(), FoldCaptures: group}}
		if _, err := exec.FoldPlanningExpression(ctx, node); err != nil {
			t.Fatal(err)
		}
		if len(group.Entries) != 1 || group.Entries[0].Value != value {
			t.Fatal("owned datum was copied or not captured")
		}
		group.Private = false
		old := &group.Entries[0]
		if _, err := exec.FoldPlanningExpression(ctx, node); err != nil {
			t.Fatal(err)
		}
		if group.Private || old != &group.Entries[0] {
			t.Fatal("owned datum hit allocated a persistent group")
		}
		return group
	}
	compoundGroup := capture(owned)
	fields[0] = sqltype.Null(fields[0].Type())
	if compoundGroup.Entries[0].Value.CompositeField(0).IsNull() || compound.CompositeField(0).IsNull() {
		t.Fatal("borrowed compound storage survived")
	}
	arrayType, ok := e.CatalogRead().LookupType(1007)
	if !ok {
		t.Fatal("missing concrete integer array")
	}
	dimensions := []sqltype.ArrayDimension{{Lower: 0, Length: 2}}
	elements := []sqltype.Value{compound.CompositeField(0), sqltype.Null(compound.CompositeField(0).Type())}
	array, err := sqltype.ArrayValue(arrayType, dimensions, elements)
	if err != nil {
		t.Fatal(err)
	}
	arrayGroup := capture(array)
	dimensions[0].Lower = 99
	elements[0] = elements[1]
	saved := arrayGroup.Entries[0].Value
	if saved.ArrayDimension(0) != (sqltype.ArrayDimension{Lower: 0, Length: 2}) || saved.ArrayElement(0).IsNull() || !saved.ArrayElement(1).IsNull() || saved.Type() != arrayType {
		t.Fatal("array scratch/NULL/provenance ownership changed after capture")
	}
}

func TestPreparedFoldOccurrenceProvenanceInputs(t *testing.T) {
	e := engine.New()
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	position := parser.Position{Offset: 17, Character: 18}
	literal := parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 7, Pos: position}}
	inner := parser.CastExpr{Child: literal, TypeName: parser.TypeName{Name: "bigint", Pos: position}, Syntax: parser.PostfixCast, Pos: parser.Position{Offset: 20, Character: 21}}
	outer := parser.CastExpr{Child: inner, TypeName: parser.TypeName{Name: "text", Pos: position}, Syntax: parser.PostfixCast, Pos: parser.Position{Offset: 28, Character: 29}}
	bound, err := b.Bind(outer, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil {
		t.Fatal(err)
	}
	cast := bound.(plan.CoercionExpr)
	child := cast.Child.(plan.CoercionExpr)
	if cast.Source == child.Source || cast.Source.Position != outer.Pos || child.Source.Position != inner.Pos {
		t.Fatal("erased casts aliased their original syntax")
	}
	// Unary lowering shares a function token but not its original written input.
	first := nullCountRaw("abs", literal)
	second := first
	second.Args = []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 9, Pos: parser.Position{Offset: 31, Character: 32}}}}
	left, err := b.Bind(first, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil {
		t.Fatal(err)
	}
	right, err := b.Bind(second, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil {
		t.Fatal(err)
	}
	if expr.InfoOf(left).Source == expr.InfoOf(right).Source {
		t.Fatal("lowered call input provenance aliased")
	}
	group := exec.FoldCaptureCandidate{}
	ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: b.BindingCatalog(), FoldCaptures: &group}}
	for _, node := range []expr.Expr{left, right} {
		if _, err := exec.FoldPlanningExpression(ctx, node); err != nil {
			t.Fatal(err)
		}
	}
	if len(group.Entries) != 2 {
		t.Fatal("distinct source occurrences collapsed")
	}
	before := group.Entries
	group.Private = false
	if _, err := exec.FoldPlanningExpression(ctx, left); err != nil {
		t.Fatal(err)
	}
	if group.Private || &before[0] != &group.Entries[0] {
		t.Fatal("stable occurrence missed")
	}
	// Equal positions/frames/usages under independent stored origins never alias.
	domain := new(ids.Domain)
	for _, origin := range []exec.FoldOrigin{
		{Owner: domain.Ref(1, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 50000}), Attribute: 1, Definition: 60000},
		{Owner: domain.Ref(1, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 50000}), Attribute: 2, Definition: 60001},
		{Owner: domain.Ref(1, ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 50001}), Attribute: 1, Revision: 2},
		{Owner: domain.Ref(2, ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 50001}), Attribute: 1, Revision: 2},
	} {
		ctx.FoldOrigin = origin
		if _, err := exec.FoldPlanningExpression(ctx, left); err != nil {
			t.Fatal(err)
		}
	}
	if len(group.Entries) != 6 {
		t.Fatal("stored origin/native provenance collapsed")
	}
	// Selected conversion routes and exact source/result usages qualify a hit.
	resultUsage := b.Type(sqltype.VarChar).Usage()
	resultUsage.Typmod = 7 // varchar(3), including PostgreSQL's header offset.
	conversion, err := sqltype.PlanCoercion(b.Type(sqltype.Int4).Usage(), resultUsage, sqltype.Explicit)
	if err != nil {
		t.Fatal(err)
	}
	integer, err := sqltype.Integer(b.Type(sqltype.Int4), 7)
	if err != nil {
		t.Fatal(err)
	}
	node := plan.CoercionExpr{Info: expr.Info{Usage: resultUsage, Pos: position}, Child: plan.LiteralExpr{Info: expr.Info{Usage: b.Type(sqltype.Int4).Usage()}, Value: integer}, Plan: conversion}
	ctx.FoldOrigin = exec.FoldOrigin{}
	if _, err := exec.FoldPlanningExpression(ctx, node); err != nil {
		t.Fatal(err)
	}
	old := group.Entries[len(group.Entries)-1]
	node.Usage.Typmod = 9 // varchar(5)
	node.Plan, err = sqltype.PlanCoercion(node.Child.ResultUsage(), node.Usage, sqltype.Explicit)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exec.FoldPlanningExpression(ctx, node); err != nil {
		t.Fatal(err)
	}
	if group.Entries[len(group.Entries)-1].Result == old.Result || old.Value.Type() != b.Type(sqltype.VarChar) {
		t.Fatal("result usage mismatch adopted or changed old datum")
	}
	t.Run("LiteralInputValidation", func(t *testing.T) {
		usage := b.Type(sqltype.Int4).Usage()
		target := b.Type(sqltype.Int8).Usage()
		conversion, err := sqltype.PlanCoercion(usage, target, sqltype.Explicit)
		if err != nil {
			t.Fatal(err)
		}
		original := plan.LiteralExpr{Info: expr.Info{Usage: usage}, Value: integer}
		node := plan.CoercionExpr{Info: expr.Info{Usage: target}, Child: original, Plan: conversion}
		group := &exec.FoldCaptureCandidate{}
		local := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: b.BindingCatalog(), FoldCaptures: group}}
		if _, err := exec.FoldPlanningExpression(local, node); err != nil {
			t.Fatal(err)
		}
		before := group.Entries[0]
		changed, err := sqltype.Integer(usage.Type, 9)
		if err != nil {
			t.Fatal(err)
		}
		for _, literal := range []plan.LiteralExpr{
			{Info: original.Info, Value: changed},
			{Info: original.Info, Value: sqltype.Value{}},
			{Info: original.Info, Value: sqltype.Null(target.Type)},
			{Info: expr.Info{Usage: usage, UnknownNull: true}, Value: integer},
		} {
			node.Child = literal
			want, wantErr := node.Eval(local)
			got, gotErr := exec.FoldPlanningExpression(local, node)
			if wantErr == nil {
				if gotErr != nil || got != want || got == before.Value {
					t.Fatal("changed physical literal inherited an old fold", got, gotErr)
				}
			} else if gotErr == nil || gotErr.Error() != wantErr.Error() {
				t.Fatal("retention changed invalid literal evaluation", gotErr, wantErr)
			}
			if wantErr != nil && !sameLiteralFold(before, group.Entries[0]) {
				t.Fatal("failed literal evaluation changed the accepted entry")
			}
			// Each case begins with the same accepted original input.
			group.Entries[0] = before
		}
	})
}

func TestPreparedFoldDemandBarriersProperties(t *testing.T) {
	e := engine.New()
	// These are real type-owned conversion nodes. Dependence metadata forbids
	// persistence even when this particular admitted input is a literal.
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	integer, err := sqltype.Integer(b.Type(sqltype.Int4), 7)
	if err != nil {
		t.Fatal(err)
	}
	conversion, err := sqltype.PlanCoercion(b.Type(sqltype.Int4).Usage(), b.Type(sqltype.Int8).Usage(), sqltype.Explicit)
	if err != nil {
		t.Fatal(err)
	}
	for _, property := range []expr.Properties{
		{RowDependent: true}, {ParameterDependent: true}, {StageDependent: true},
		{DomainDependent: true}, {RuntimeDomainConversion: true},
		{Volatility: expr.Stable}, {Volatility: expr.Volatile},
	} {
		group := exec.FoldCaptureCandidate{}
		node := plan.CoercionExpr{Info: expr.Info{Usage: b.Type(sqltype.Int8).Usage(), Property: property}, Child: plan.LiteralExpr{Info: expr.Info{Usage: b.Type(sqltype.Int4).Usage()}, Value: integer}, Plan: conversion}
		if _, err := exec.FoldPlanningExpression(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: b.BindingCatalog(), FoldCaptures: &group}}, node); err != nil {
			t.Fatal(err)
		}
		if group.Private || group.Entries != nil {
			t.Fatal("dependence barrier persisted a value", property)
		}
	}

}

func nullCountRaw(name string, args ...parser.Expr) parser.FunctionExpr {
	return parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Args: args, Pos: parser.Position{Character: 8}}
}
