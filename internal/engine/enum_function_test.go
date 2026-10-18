package engine

import (
	"context"
	"strings"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestEnumFunctionRetagAndStoredProviders(t *testing.T) {
	ctx := context.Background()
	source := New()
	mood, _ := sqltype.Enum(45000, "mood", "public.mood", []string{"z", "a", "m"})
	array, _ := sqltype.Array(45001, "_mood", mood)
	targetMood, _ := sqltype.Enum(45002, "mood", "public.mood", mood.EnumLabels())
	targetArray, _ := sqltype.Array(45003, "_mood", targetMood)
	fixtureView := func(e *Engine, enum, array *sqltype.Descriptor) CatalogRead {
		base := e.CatalogRead()
		namespace := base.state.names[ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"}]
		stage := e.BeginCatalogStage(base)
		for _, d := range []*typeDescriptor{{typ: enum, namespace: namespace, revision: 1, array: array.ID()}, {typ: array, namespace: namespace, revision: 1}} {
			if err := stage.Create(d); err != nil {
				t.Fatal(err)
			}
		}
		return e.stageRead(stage)
	}
	sourceView := fixtureView(source, mood, array)
	targetView := fixtureView(New(), targetMood, targetArray)
	plan, err := sqltype.BindEnumSupport("enum_range", array.Usage(), mood.Usage(), mood.Usage())
	if err != nil {
		t.Fatal(err)
	}
	literal := func(value sqltype.Value) BoundExpr {
		return literalExpression{expressionInfo: expressionInfo{usage: value.Type().Usage()}, value: value}
	}
	expr := functions.Enum[BoundExpr, *EvalContext, ExprProperties]{Info: functionInfo(expressionInfo{usage: array.Usage(), properties: ExprProperties{Volatility: Stable}}), Plan: plan, Args: []BoundExpr{literal(sqltype.Null(mood)), literal(sqltype.Null(mood))}, EnumName: "mood"}
	old, err := expr.Eval(&EvalContext{Context: ctx})
	if err != nil {
		t.Fatal(err)
	}
	identity := func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }
	unchanged, changed, err := retagBoundTypes(sourceView, expr, identity)
	if err != nil || changed || unchanged.(functions.Enum[BoundExpr, *EvalContext, ExprProperties]).Args[0] != expr.Args[0] {
		t.Fatal("unchanged mapping", changed, err)
	}
	mapping := func(d *sqltype.Descriptor) *sqltype.Descriptor {
		if d == mood {
			return targetMood
		}
		if d == array {
			return targetArray
		}
		return d
	}
	if _, _, err := retagBoundTypes(targetView, expr, mapping); err == nil {
		t.Fatal("cross-lifetime literal remap admitted without import authority")
	}
	targetView.state.scalarImports = map[*sqltype.Descriptor]*sqltype.Descriptor{mood: targetMood}
	mapped, changed, err := retagBoundTypes(targetView, expr, mapping)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	next := mapped.(functions.Enum[BoundExpr, *EvalContext, ExprProperties])
	value, err := next.Eval(&EvalContext{Context: ctx})
	if err != nil || value.Type() != targetArray || value.ArrayElement(0).Type() != targetMood || next.Plan.Enum() != targetMood {
		t.Fatal(value, err)
	}
	if old.Type() != array || old.ArrayElement(0).Type() != mood || expr.Plan.Enum() != mood || expr.Args[0].ResultUsage().Type != mood {
		t.Fatal("source mutated")
	}
	text, deps, err := renderColumnDefault(ctx, targetView, next)
	if err != nil || !strings.Contains(text, "pg_catalog.enum_range(") || len(deps) < 2 {
		t.Fatal(text, deps, err)
	}
	forged := next
	forged.Plan = plan
	if _, err := (scalarRenderer{}).scalarFunctionSQL(ctx, targetView, forged, targetArray.Usage()); err == nil {
		t.Fatal("foreign stored provider accepted")
	}
	if !sameGroupExpression(expr, expr) || sameGroupExpression(expr, next) {
		t.Fatal("provider equality")
	}
	var children int
	boundExpressionChildren(expr, func(BoundExpr) { children++ })
	if children != 2 {
		t.Fatal("lost children", children)
	}
	wrapped, err := sqltype.Domain(45004, "wrapped", "public.wrapped", mood.Usage())
	if err != nil {
		t.Fatal(err)
	}
	projection, err := sqltype.Array(45005, "_wrapped", wrapped)
	if err != nil || !projection.Concrete() || projection.Element() != wrapped {
		t.Fatal("domain-over-enum projection regressed", err)
	}
	if plannedVolatility(expr) != Stable {
		t.Fatal("intrinsic volatility")
	}
	for _, signature := range []struct {
		name  string
		array bool
		arity int
	}{
		{"enum_first", false, 1},
		{"enum_last", false, 1},
		{"enum_range", true, 1},
		{"enum_range", true, 2},
	} {
		result := mood.Usage()
		if signature.array {
			result = array.Usage()
		}
		usages := make([]sqltype.Usage, signature.arity)
		arguments := make([]BoundExpr, signature.arity)
		for i := range usages {
			usages[i] = mood.Usage()
			arguments[i] = literal(sqltype.Null(mood))
		}
		provider, err := sqltype.BindEnumSupport(signature.name, result, usages...)
		if err != nil {
			t.Fatal(err)
		}
		call := functions.Enum[BoundExpr, *EvalContext, ExprProperties]{Info: functionInfo(expressionInfo{usage: result}), Plan: provider, Args: arguments, EnumName: "mood"}
		if generationImmutable(call) || plannedVolatility(call) != Stable {
			t.Fatalf("%s/%d lost intrinsic STABLE admission", signature.name, signature.arity)
		}
	}
	if !conditionalConstantInputs(expr) {
		t.Fatal("enum closedness")
	}
	simplified, changed, err := simplifyConditional(&EvalContext{Context: ctx}, expr, false)
	if err != nil || changed {
		t.Fatal("STABLE call folded", changed, err)
	}
	if _, ok := simplified.(functions.Enum[BoundExpr, *EvalContext, ExprProperties]); !ok {
		t.Fatal("lost STABLE call")
	}
	var order []int
	first, _ := sqltype.BindEnumSupport("enum_first", mood.Usage(), mood.Usage())
	unary := functions.Enum[BoundExpr, *EvalContext, ExprProperties]{Info: functionInfo(expressionInfo{usage: mood.Usage()}), Plan: first, Args: []BoundExpr{mathObservedArgument{expressionInfo: expressionInfo{usage: mood.Usage()}, value: sqltype.Null(mood), order: &order, index: 0}}, EnumName: "mood"}
	volatile := unary
	volatile.Args = []BoundExpr{mathObservedArgument{expressionInfo: expressionInfo{usage: mood.Usage(), properties: ExprProperties{Volatility: Volatile}}, value: sqltype.Null(mood), order: &order}}
	if plannedVolatility(volatile) != Volatile {
		t.Fatal("child volatility lost")
	}
}
