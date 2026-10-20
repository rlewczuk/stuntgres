package engine

import (
	"context"
	"strings"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestExtensionVersionPathsConsumers(t *testing.T) {
	e := New()
	raw := parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "fuzzystrmatch"}}
	node := catalogRelationsUnitBind(t, e, "pg_extension_update_paths", raw)
	call := node.(extensionPathsExpression)
	if err := call.validate(); err != nil {
		t.Fatal(err)
	}
	if call.Properties().Volatility != expr.Stable || call.arg.ResultUsage().Type.ID() != sqltype.Name || !sourceReturnsSet(call) || !sourceAdmitted(call) {
		t.Fatal("typed SRF contract")
	}
	count := 0
	boundExpressionChildren(call, func(expr.Expr) { count++ })
	if count != 1 {
		t.Fatal("path argument traversal", count)
	}
	mapped, err := remapBoundExpression(call, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
	if err != nil || !sameGroupExpression(call, mapped) {
		t.Fatal("path reference remap", err)
	}
	demand := false
	text, err := (scalarRenderer{commentSnapshot: &demand}).scalarFunctionSQL(context.Background(), e.CatalogRead(), mapped)
	if err != nil || !strings.Contains(text, "pg_catalog.pg_extension_update_paths(") || !demand {
		t.Fatal("path symbolic render and demand", text, err)
	}
	if err := validateGenerationPlan(e.CatalogRead(), nil, mapped); err != nil {
		t.Fatal("persisted typed plan validation", err)
	}
	args := expr.Expr(call).(expr.Composite).Children()
	reconstructed, err := expr.Substitute(expr.Expr(call).(expr.Composite), expr.InfoOf(call), args)
	if err != nil || !sameGroupExpression(call, reconstructed) {
		t.Fatal("path reconstruction", err)
	}
	if _, err := expr.Substitute(expr.Expr(call).(expr.Composite), expr.InfoOf(call), nil); err == nil {
		t.Fatal("missing reconstructed argument admitted")
	}
	binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	requests := []expr.Expr{call}
	root, err := binder.lowerQuerySets(&plan.SingletonNode{}, requests, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	stage, ok := root.(*plan.SetProjectNode)
	if !ok || len(stage.Calls) != 1 || !sameQuerySetOccurrence(call, stage.Calls[0]) {
		t.Fatal("record set lowering lost occurrence")
	}
	if err := validateQuerySetSteps(stage.Steps, len(stage.Calls), len(stage.Scalars)); err != nil {
		t.Fatal(err)
	}
	fresh := New()
	destination := catalogRelationsUnitBind(t, fresh, "pg_extension_update_paths", raw).(extensionPathsExpression)
	retagged, _, err := retagBoundTypes(fresh.CatalogRead(), call, func(d *sqltype.Descriptor) *sqltype.Descriptor {
		if d.ID() == 2249 {
			return destination.ResultUsage().Type
		}
		mapped, _ := fresh.CatalogRead().LookupType(d.ID())
		return mapped
	})
	if err != nil {
		t.Fatal(err)
	}
	next := retagged.(extensionPathsExpression)
	// Anonymous RECORD descriptors belong to occurrences. The shared retagger
	// reconstructs a new shape from destination-owned fields rather than
	// adopting another bound call's occurrence pointer.
	nameType, _ := fresh.CatalogRead().LookupType(sqltype.Name)
	textType, _ := fresh.CatalogRead().LookupType(sqltype.Text)
	if next.validate() != nil || !sqltype.SameRecordUsage(next.ResultUsage(), destination.ResultUsage()) || next.ResultUsage().Type == call.ResultUsage().Type || next.arg.ResultUsage().Type != nameType {
		t.Fatal("path occurrence or destination argument provenance lost")
	}
	for i := range next.ResultUsage().Type.CompositeLen() {
		if next.ResultUsage().Type.CompositeMember(i).Usage.Type != textType {
			t.Fatal("path record retained foreign field type", i)
		}
	}
}
