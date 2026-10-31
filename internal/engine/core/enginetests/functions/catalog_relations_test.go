package functions_test

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestCatalogRelationsBinding(t *testing.T) {
	e := engine.New()
	scalar := catalogRelationsUnitBind(t, e, "pg_get_userbyid", parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}).(functions.CatalogUserName)
	set := catalogRelationsUnitBind(t, e, "pg_get_catalog_foreign_keys").(functions.CatalogForeignKeys)
	if scalar.Validate() != nil || set.Validate() != nil {
		t.Fatal("invalid bound signature")
	}
	for _, sample := range []struct {
		node expr.Expr
		oid  uint32
	}{{scalar, 1642}, {set, 6159}} {
		reference := expr.InfoOf(sample.node).RoutineReference
		if reference == nil || reference.RoutineOID() != sample.oid {
			t.Fatal("wrong original declaration", reference)
		}
		if exec.PlannedVolatility(sample.node) != expr.Stable || plan.GenerationImmutable(sample.node) || exec.ConditionalConstantInputs(sample.node) {
			t.Fatal("catalog inquiry lost STABLE symbolic semantics")
		}
	}
	value, err := scalar.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
	if err != nil || !value.IsNull() || value.Type().ID() != sqltype.Name {
		t.Fatal("strict typed null", value, err)
	}
	if scalar.Arg.ResultUsage().Type.ID() != sqltype.Oid || scalar.ResultUsage().Type.ID() != sqltype.Name || !exec.SourceReturnsSet(set) || exec.SourceReturnsSet(scalar) {
		t.Fatal("physical signature")
	}
	for _, declaration := range routine.BuiltinRoutineDeclarations {
		if declaration.Identity.ID == 1642 || declaration.Identity.ID == 6159 {
			if !declaration.Strict || declaration.Volatility != expr.Stable || declaration.Parallel != 's' || declaration.ReturnsSet != (declaration.Identity.ID == 6159) {
				t.Fatal("release declaration flags", declaration)
			}
		}
	}
}

func TestCatalogRelationsConsumers(t *testing.T) {
	e := engine.New()
	scalar := catalogRelationsUnitBind(t, e, "pg_get_userbyid", parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}})
	set := catalogRelationsUnitBind(t, e, "pg_get_catalog_foreign_keys")
	for _, sample := range []struct {
		node     expr.Expr
		name     string
		children int
	}{{scalar, "pg_get_userbyid", 1}, {set, "pg_get_catalog_foreign_keys", 0}} {
		count := 0
		plan.ExpressionChildren(sample.node, func(expr.Expr) { count++ })
		if count != sample.children {
			t.Fatal("typed child traversal", sample.name, count)
		}
		mapped, err := stored.RemapExpression(sample.node, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
		if err != nil || !analyze.SameGroupExpression(sample.node, mapped) {
			t.Fatal("stored remap", sample.name, err)
		}
		demand := false
		text, err := (stored.ScalarRenderer{CommentSnapshot: &demand}).ScalarFunctionSQL(context.Background(), e.CatalogRead(), mapped)
		if err != nil || !strings.Contains(text, "pg_catalog."+sample.name+"(") || !demand {
			t.Fatal("symbolic serialization and catalog demand", text, demand, err)
		}
		if err := table.ValidateGenerationPlan(e.CatalogRead(), nil, mapped); err != nil {
			t.Fatal("typed persisted plan validation", err)
		}
		args := expr.Expr(sample.node).(expr.Composite).Children()
		reconstructed, err := expr.Substitute(expr.Expr(sample.node).(expr.Composite), expr.InfoOf(sample.node), args)
		if err != nil || !analyze.SameGroupExpression(sample.node, reconstructed) {
			t.Fatal("source reconstruction", err)
		}
	}
	// Target-list record expansion must identify the same selected occurrence
	// while constructing both the call slots and their execution schedule.
	binder := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	requests := []expr.Expr{set}
	root, err := binder.LowerQuerySets(&plan.SingletonNode{}, requests, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	stage, ok := root.(*plan.SetProjectNode)
	if !ok {
		t.Fatal("catalog set was not lowered")
	}
	if err := exec.ValidateQuerySetSteps(stage.Steps, len(stage.Calls), len(stage.Scalars)); err != nil {
		t.Fatal("catalog set occurrence lost its schedule", err)
	}
	if _, ok := requests[0].(plan.ColumnExpr); !ok {
		t.Fatal("catalog set occurrence was not replaced by its owned slot")
	}
	fresh := engine.New()
	destination := catalogRelationsUnitBind(t, fresh, "pg_get_catalog_foreign_keys").(functions.CatalogForeignKeys)
	mapped, _, err := stored.RetagTypes(fresh.CatalogRead(), set, func(d *sqltype.Descriptor) *sqltype.Descriptor {
		if d.ID() == 2249 {
			return destination.ResultUsage().Type
		}
		value, _ := fresh.CatalogRead().LookupType(d.ID())
		return value
	})
	if err != nil {
		t.Fatal(err)
	}
	retagged := mapped.(functions.CatalogForeignKeys)
	if retagged.Validate() != nil {
		t.Fatal("invalid retagged metadata")
	}
	originalSource, err := exec.OpenFunctionSource(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}, set)
	if err != nil {
		t.Fatal(err)
	}
	defer originalSource.Close()
	retaggedSource, err := exec.OpenFunctionSource(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}, retagged)
	if err != nil {
		t.Fatal(err)
	}
	defer retaggedSource.Close()
	if originalSource.State == retaggedSource.State {
		t.Fatal("retag reused foreign descriptor metadata")
	}
	for i := 0; ; i++ {
		value, present, err := retaggedSource.Next(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !present {
			break
		}
		for j := range 6 {
			if value.CompositeField(j).Type() != retagged.Usage.Type.CompositeMember(j).Usage.Type {
				t.Fatal("foreign typed metadata", i, j)
			}
		}
	}
}

func catalogRelationsUnitBind(t *testing.T, e *core.Engine, name string, args ...parser.Expr) expr.Expr {
	t.Helper()
	binder := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	binder.Clause.AggregateClause = "SELECT"
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Args: args}
	node, err := binder.BindFunctionProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	return node
}
