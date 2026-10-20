package engine

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func jsonElementsUnitMarker(t *testing.T, b *expressionBinder, name, input string) functions.JSONElements {
	t.Helper()
	b.clause.aggregateClause = "SELECT"
	b.query.namespace = &queryNamespace{}
	node, err := b.bindFunction(nullCountRaw(name, parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: input}}))
	if err != nil {
		t.Fatal(err)
	}
	marker, ok := node.(functions.JSONElements)
	if !ok {
		t.Fatalf("missing scalar marker: %T", node)
	}
	return marker
}

func TestJSONElementsMarkerRetagAndTraversal(t *testing.T) {
	engine := New()
	view := engine.CatalogRead()
	for _, name := range []string{"json_array_elements", "json_array_elements_text"} {
		b := newTestExpressionBinder(t, context.Background(), engine, sqltype.InputEnvironment{}, nil)
		original := jsonElementsUnitMarker(t, b, name, `[1]`)
		id := sqltype.JSON
		if name == "json_array_elements_text" {
			id = sqltype.Text
		}
		if original.ResultUsage().Type != b.typ(id) || original.ResultUsage().Type.IsComposite() || !validExpressionUsage(view, original.ResultUsage()) {
			t.Fatal("scalar result usage", original)
		}
		if !original.Properties().StageDependent || original.Properties().Volatility != expr.Immutable || !b.clause.queryHasTargetSet || !sourceReturnsSet(original) || querySetDepth(original) != 1 {
			t.Fatal("set properties", original)
		}
		fresh := jsonObjectUnitTypes()
		mapped, changed, err := retagBoundTypes(view, original, func(d *sqltype.Descriptor) *sqltype.Descriptor { return fresh[d.ID()] })
		if err != nil || !changed {
			t.Fatal("retag", changed, err)
		}
		next := mapped.(functions.JSONElements)
		if next.ResultUsage().Type != fresh[id] || next.Args[0].ResultUsage().Type != fresh[sqltype.JSON] || next.Plan.ResultUsage() != next.ResultUsage() || next.SourcePosition() != original.SourcePosition() || next.Properties() != original.Properties() {
			t.Fatal("mixed-generation marker", next)
		}
		if original.ResultUsage().Type != b.typ(id) || original.Args[0].ResultUsage().Type != b.typ(sqltype.JSON) {
			t.Fatal("retained marker mutated")
		}
		cursor, err := openFunctionSource(&EvalContext{evalStatement: evalStatement{Context: context.Background()}}, next)
		if err != nil {
			t.Fatal(err)
		}
		value, present, err := cursor.Next(context.Background())
		cursor.Close()
		if err != nil || !present || value.Type() != fresh[id] {
			t.Fatal("retagged provider", value, present, err)
		}
		var dependencies []expr.Expr
		walkBoundExpression(original, func(n expr.Expr) {
			if n == original.Args[0] {
				dependencies = append(dependencies, n)
			}
		})
		if len(dependencies) != 1 || dependencies[0] != original.Args[0] || plannedVolatility(original) != expr.Immutable {
			t.Fatal("lost argument dependency", dependencies)
		}
		clone, err := remapBoundExpression(original, func(r ids.ObjectRef) ids.ObjectRef { return r }, nil)
		if err != nil || !sameGroupExpression(original, clone) {
			t.Fatal("stored remap", clone, err)
		}
		folded, _, err := preflightCoercions(&EvalContext{evalStatement: evalStatement{Context: context.Background()}}, original)
		if err != nil || !sourceReturnsSet(folded) {
			t.Fatal("preflight folded set", folded, err)
		}
		folded, _, err = simplifyConditional(&EvalContext{evalStatement: evalStatement{Context: context.Background()}}, original, false)
		if err != nil || !sourceReturnsSet(folded) {
			t.Fatal("simplifier folded set", folded, err)
		}
		text, err := (pgRenderer{view: view}).sql(context.Background(), original, pgRenderParent{})
		if err != nil || !strings.Contains(text, name+"(") || !strings.Contains(text, "::json") {
			t.Fatal("symbolic rendering", text, err)
		}
		other := jsonElementsUnitMarker(t, b, name, `[2]`)
		root := &functionFromNode{Calls: []expr.Expr{original}, querySchema: querySchema{{Name: "value", Usage: b.typ(id).Usage()}}, Frame: 1}
		rewritten, err := rewriteQueryExpressions(root, func(n expr.Expr) (expr.Expr, bool, error) {
			if n == original.Args[0] {
				return other.Args[0], true, nil
			}
			return n, false, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if rewritten.(*functionFromNode).Calls[0].(functions.JSONElements).Args[0] != other.Args[0] || root.Calls[0].(functions.JSONElements).Args[0] != original.Args[0] {
			t.Fatal("query rewrite mutated original")
		}
		if err := validateFunctionFromLayout(root); err != nil {
			t.Fatal(err)
		}
		if functionFromCallWidth(original) != 1 || sourceOutName(original) != "value" {
			t.Fatal("scalar OUT layout", original)
		}
		if sourceOutName(original.Args[0]) != "" {
			t.Fatal("OUT naming escaped selected function")
		}
	}
}

func TestJSONElementsNullPresenceAndPadding(t *testing.T) {
	engine := New()
	b := newTestExpressionBinder(t, context.Background(), engine, sqltype.InputEnvironment{}, nil)
	for _, name := range []string{"json_array_elements", "json_array_elements_text"} {
		marker := jsonElementsUnitMarker(t, b, name, `[null]`)
		series, err := b.bindFunction(nullCountRaw("generate_series", parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 7}}, parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 8}}))
		if err != nil {
			t.Fatal(err)
		}
		calls := []expr.Expr{series, marker}
		invocation, err := openFunctionInvocation(&EvalContext{evalStatement: evalStatement{Context: context.Background()}}, &functionFromNode{Calls: calls})
		if err != nil {
			t.Fatal(err)
		}
		row := make([]sqltype.Value, 2)
		present, err := invocation.nextFrom(context.Background(), calls, row)
		if err != nil || !present || row[1].IsNull() != (name == "json_array_elements_text") || row[1].Type() != marker.ResultUsage().Type {
			t.Fatal("null presence", row, present, err)
		}
		if n, _ := row[0].Integer(); n != 7 {
			t.Fatal("first sibling", row)
		}
		present, err = invocation.nextFrom(context.Background(), calls, row)
		if err != nil || !present || !row[1].IsNull() || row[1].Type() != marker.ResultUsage().Type {
			t.Fatal("typed padding", row, present, err)
		}
		if n, _ := row[0].Integer(); n != 8 {
			t.Fatal("second sibling", row)
		}
		present, err = invocation.nextFrom(context.Background(), calls, row)
		invocation.close()
		if err != nil || present {
			t.Fatal("exhausted source", present, err)
		}
		node := regexpSetUnitLayout(&setProjectNode{querySchema: querySchema{{Usage: marker.ResultUsage()}}, Child: &singletonNode{}, Calls: []expr.Expr{marker}, Frame: 1})
		stage, err := openSetProjectNode(context.Background(), &queryState{source: &querySource{}, frame: 1}, node)
		if err != nil {
			t.Fatal(err)
		}
		target := make([]sqltype.Value, 1)
		present, err = stage.Next(context.Background(), target)
		stage.Close()
		if err != nil || !present || target[0].Type() != marker.ResultUsage().Type || target[0].IsNull() != (name == "json_array_elements_text") {
			t.Fatal("scalar target slot", target, present, err)
		}
	}
}
