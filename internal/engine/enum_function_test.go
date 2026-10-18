package engine

import (
	"context"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
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
	fixtureView := func(e *Engine, enum, array *sqltype.Descriptor) catalog.Read {
		base := e.CatalogRead()
		namespace := base.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"})
		stage := e.BeginCatalogStage(base)
		for _, d := range []*catalog.Type{{SQLType: enum, ObjectNamespace: namespace, ObjectRevision: 1, Array: array.ID()}, {SQLType: array, ObjectNamespace: namespace, ObjectRevision: 1}} {
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
	literal := func(value sqltype.Value) expr.Expr {
		return literalExpression{Info: expr.Info{Usage: value.Type().Usage()}, value: value}
	}
	bound := functions.Enum{Info: expr.Info{Usage: array.Usage(), Property: expr.Properties{Volatility: expr.Stable}}, Plan: plan, Args: []expr.Expr{literal(sqltype.Null(mood)), literal(sqltype.Null(mood))}, EnumName: "mood"}
	old, err := bound.Eval(&EvalContext{Context: ctx})
	if err != nil {
		t.Fatal(err)
	}
	identity := func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }
	unchanged, changed, err := retagBoundTypes(sourceView, bound, identity)
	if err != nil || changed || unchanged.(functions.Enum).Args[0] != bound.Args[0] {
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
	if _, _, err := retagBoundTypes(targetView, bound, mapping); err == nil {
		t.Fatal("cross-lifetime literal remap admitted without import authority")
	}
	targetView.State().SetScalarImports(map[*sqltype.Descriptor]*sqltype.Descriptor{mood: targetMood})
	mapped, changed, err := retagBoundTypes(targetView, bound, mapping)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	next := mapped.(functions.Enum)
	value, err := next.Eval(&EvalContext{Context: ctx})
	if err != nil || value.Type() != targetArray || value.ArrayElement(0).Type() != targetMood || next.Plan.Enum() != targetMood {
		t.Fatal(value, err)
	}
	if old.Type() != array || old.ArrayElement(0).Type() != mood || bound.Plan.Enum() != mood || bound.Args[0].ResultUsage().Type != mood {
		t.Fatal("source mutated")
	}
	text, deps, err := renderColumnDefault(ctx, targetView, next)
	if err != nil || !strings.Contains(text, "pg_catalog.enum_range(") || len(deps) < 2 {
		t.Fatal(text, deps, err)
	}
	forged := next
	forged.Plan = plan
	if _, err := (scalarRenderer{}).scalarFunctionSQL(ctx, targetView, forged); err == nil {
		t.Fatal("foreign stored provider accepted")
	}
	if !sameGroupExpression(bound, bound) || sameGroupExpression(bound, next) {
		t.Fatal("provider equality")
	}
	var children int
	boundExpressionChildren(bound, func(expr.Expr) { children++ })
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
	if plannedVolatility(bound) != expr.Stable {
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
		arguments := make([]expr.Expr, signature.arity)
		for i := range usages {
			usages[i] = mood.Usage()
			arguments[i] = literal(sqltype.Null(mood))
		}
		provider, err := sqltype.BindEnumSupport(signature.name, result, usages...)
		if err != nil {
			t.Fatal(err)
		}
		call := functions.Enum{Info: expr.Info{Usage: result}, Plan: provider, Args: arguments, EnumName: "mood"}
		if generationImmutable(call) || plannedVolatility(call) != expr.Stable {
			t.Fatalf("%s/%d lost intrinsic STABLE admission", signature.name, signature.arity)
		}
	}
	if !conditionalConstantInputs(bound) {
		t.Fatal("enum closedness")
	}
	simplified, changed, err := simplifyConditional(&EvalContext{Context: ctx}, bound, false)
	if err != nil || changed {
		t.Fatal("STABLE call folded", changed, err)
	}
	if _, ok := simplified.(functions.Enum); !ok {
		t.Fatal("lost STABLE call")
	}
	var order []int
	first, _ := sqltype.BindEnumSupport("enum_first", mood.Usage(), mood.Usage())
	unary := functions.Enum{Info: expr.Info{Usage: mood.Usage()}, Plan: first, Args: []expr.Expr{mathObservedArgument{Info: expr.Info{Usage: mood.Usage()}, value: sqltype.Null(mood), order: &order, index: 0}}, EnumName: "mood"}
	volatile := unary
	volatile.Args = []expr.Expr{mathObservedArgument{Info: expr.Info{Usage: mood.Usage(), Property: expr.Properties{Volatility: expr.Volatile}}, value: sqltype.Null(mood), order: &order}}
	if plannedVolatility(volatile) != expr.Volatile {
		t.Fatal("child volatility lost")
	}
}
