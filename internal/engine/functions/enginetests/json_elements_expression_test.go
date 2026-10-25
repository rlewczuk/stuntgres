package enginetests_test

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func jsonElementsUnitMarker(t *testing.T, b *analyze.Binder, name, input string) functions.JSONElements {
	t.Helper()
	b.Clause.AggregateClause = "SELECT"
	b.Query.Namespace = &analyze.QueryNamespace{}
	node, err := b.BindFunction(nullCountRaw(name, parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: input}}))
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
	eng := engine.New()
	view := eng.CatalogRead()
	for _, name := range []string{"json_array_elements", "json_array_elements_text"} {
		b := enginetest.ExpressionBinder(t, context.Background(), eng, sqltype.InputEnvironment{}, nil)
		original := jsonElementsUnitMarker(t, b, name, `[1]`)
		id := sqltype.JSON
		if name == "json_array_elements_text" {
			id = sqltype.Text
		}
		if original.ResultUsage().Type != b.Type(id) || original.ResultUsage().Type.IsComposite() || !stored.ValidExpressionUsage(view, original.ResultUsage()) {
			t.Fatal("scalar result usage", original)
		}
		if !original.Properties().StageDependent || original.Properties().Volatility != expr.Immutable || !b.Clause.QueryHasTargetSet || !exec.SourceReturnsSet(original) || exec.QuerySetDepth(original) != 1 {
			t.Fatal("set properties", original)
		}
		fresh := jsonObjectUnitTypes()
		mapped, changed, err := stored.RetagTypes(view, original, func(d *sqltype.Descriptor) *sqltype.Descriptor { return fresh[d.ID()] })
		if err != nil || !changed {
			t.Fatal("retag", changed, err)
		}
		next := mapped.(functions.JSONElements)
		if next.ResultUsage().Type != fresh[id] || next.Args[0].ResultUsage().Type != fresh[sqltype.JSON] || next.Plan.ResultUsage() != next.ResultUsage() || next.SourcePosition() != original.SourcePosition() || next.Properties() != original.Properties() {
			t.Fatal("mixed-generation marker", next)
		}
		if original.ResultUsage().Type != b.Type(id) || original.Args[0].ResultUsage().Type != b.Type(sqltype.JSON) {
			t.Fatal("retained marker mutated")
		}
		cursor, err := exec.OpenFunctionSource(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}, next)
		if err != nil {
			t.Fatal(err)
		}
		value, present, err := cursor.Next(context.Background())
		cursor.Close()
		if err != nil || !present || value.Type() != fresh[id] {
			t.Fatal("retagged provider", value, present, err)
		}
		var dependencies []expr.Expr
		plan.WalkExpression(original, func(n expr.Expr) {
			if n == original.Args[0] {
				dependencies = append(dependencies, n)
			}
		})
		if len(dependencies) != 1 || dependencies[0] != original.Args[0] || exec.PlannedVolatility(original) != expr.Immutable {
			t.Fatal("lost argument dependency", dependencies)
		}
		clone, err := stored.RemapExpression(original, func(r ids.ObjectRef) ids.ObjectRef { return r }, nil)
		if err != nil || !analyze.SameGroupExpression(original, clone) {
			t.Fatal("stored remap", clone, err)
		}
		folded, _, err := exec.PreflightCoercions(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}, original)
		if err != nil || !exec.SourceReturnsSet(folded) {
			t.Fatal("preflight folded set", folded, err)
		}
		folded, _, err = exec.SimplifyConditional(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}, original, false)
		if err != nil || !exec.SourceReturnsSet(folded) {
			t.Fatal("simplifier folded set", folded, err)
		}
		text, err := (stored.Deparser{View: view}).SQL(context.Background(), original, stored.DeparseParent{})
		if err != nil || !strings.Contains(text, name+"(") || !strings.Contains(text, "::json") {
			t.Fatal("symbolic rendering", text, err)
		}
		other := jsonElementsUnitMarker(t, b, name, `[2]`)
		root := &plan.FunctionFromNode{Calls: []expr.Expr{original}, QuerySchema: plan.QuerySchema{{Name: "value", Usage: b.Type(id).Usage()}}, Frame: 1}
		rewritten, err := plan.RewriteQueryExpressions(root, func(n expr.Expr) (expr.Expr, bool, error) {
			if n == original.Args[0] {
				return other.Args[0], true, nil
			}
			return n, false, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if rewritten.(*plan.FunctionFromNode).Calls[0].(functions.JSONElements).Args[0] != other.Args[0] || root.Calls[0].(functions.JSONElements).Args[0] != original.Args[0] {
			t.Fatal("query rewrite mutated original")
		}
		if err := exec.ValidateFunctionFromLayout(root); err != nil {
			t.Fatal(err)
		}
		if exec.FunctionFromCallWidth(original) != 1 || expr.SourceOutName(original) != "value" {
			t.Fatal("scalar OUT layout", original)
		}
		if expr.SourceOutName(original.Args[0]) != "" {
			t.Fatal("OUT naming escaped selected function")
		}
	}
}

func TestJSONElementsNullPresenceAndPadding(t *testing.T) {
	eng := engine.New()
	b := enginetest.ExpressionBinder(t, context.Background(), eng, sqltype.InputEnvironment{}, nil)
	for _, name := range []string{"json_array_elements", "json_array_elements_text"} {
		marker := jsonElementsUnitMarker(t, b, name, `[null]`)
		series, err := b.BindFunction(nullCountRaw("generate_series", parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 7}}, parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 8}}))
		if err != nil {
			t.Fatal(err)
		}
		calls := []expr.Expr{series, marker}
		invocation, err := exec.OpenFunctionInvocation(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}, &plan.FunctionFromNode{Calls: calls})
		if err != nil {
			t.Fatal(err)
		}
		row := make([]sqltype.Value, 2)
		present, err := invocation.NextFrom(context.Background(), calls, row)
		if err != nil || !present || row[1].IsNull() != (name == "json_array_elements_text") || row[1].Type() != marker.ResultUsage().Type {
			t.Fatal("null presence", row, present, err)
		}
		if n, _ := row[0].Integer(); n != 7 {
			t.Fatal("first sibling", row)
		}
		present, err = invocation.NextFrom(context.Background(), calls, row)
		if err != nil || !present || !row[1].IsNull() || row[1].Type() != marker.ResultUsage().Type {
			t.Fatal("typed padding", row, present, err)
		}
		if n, _ := row[0].Integer(); n != 8 {
			t.Fatal("second sibling", row)
		}
		present, err = invocation.NextFrom(context.Background(), calls, row)
		invocation.Close()
		if err != nil || present {
			t.Fatal("exhausted source", present, err)
		}
		node := regexpSetUnitLayout(&plan.SetProjectNode{QuerySchema: plan.QuerySchema{{Usage: marker.ResultUsage()}}, Child: &plan.SingletonNode{}, Calls: []expr.Expr{marker}, Frame: 1})
		stage, err := exec.OpenSetProjectNode(context.Background(), &exec.State{Source: &exec.Source{}, Frame: 1}, node)
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
