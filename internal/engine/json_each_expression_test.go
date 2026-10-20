package engine

import (
	"context"
	"strings"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func jsonEachUnitMarker(t *testing.T, b *expressionBinder, name, input string) functions.JSONEach {
	t.Helper()
	b.clause.aggregateClause = "SELECT"
	b.query.namespace = &queryNamespace{}
	node, err := b.bindFunction(nullCountRaw(name, parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: input}}))
	if err != nil {
		t.Fatal(err)
	}
	marker, ok := node.(functions.JSONEach)
	if !ok {
		t.Fatalf("missing record marker: %T", node)
	}
	return marker
}

func TestJSONEachMarkerRetagWalkAndReconstruction(t *testing.T) {
	engine := New()
	view := engine.CatalogRead()
	for _, name := range []string{"json_each", "json_each_text", "jsonb_each", "jsonb_each_text"} {
		b := newTestExpressionBinder(t, context.Background(), engine, sqltype.InputEnvironment{}, nil)
		original := jsonEachUnitMarker(t, b, name, `{"a":1}`)
		inputID, valueID := sqltype.JSON, sqltype.JSON
		if strings.HasPrefix(name, "jsonb_") {
			inputID, valueID = sqltype.JSONB, sqltype.JSONB
		}
		if strings.HasSuffix(name, "_text") {
			valueID = sqltype.Text
		}
		shape := original.ResultUsage().Type
		if shape.ID() != 2249 || shape.CompositeLen() != 2 || shape.CompositeMember(0).Name != "key" || shape.CompositeMember(1).Name != "value" || shape.CompositeMember(0).Usage.Type != b.typ(sqltype.Text) || shape.CompositeMember(1).Usage.Type != b.typ(valueID) || !validExpressionUsage(view, original.ResultUsage()) {
			t.Fatal("analysis shape", shape)
		}
		if !original.Properties().StageDependent || original.Properties().Volatility != expr.Immutable || !b.clause.queryHasTargetSet || !exec.SourceReturnsSet(original) || exec.QuerySetDepth(original) != 1 {
			t.Fatal("set properties", original)
		}
		fresh := jsonObjectUnitTypes()
		mapped, changed, err := retagBoundTypes(view, original, func(d *sqltype.Descriptor) *sqltype.Descriptor { return fresh[d.ID()] })
		if err != nil || !changed {
			t.Fatal("record remap", changed, err)
		}
		next := mapped.(functions.JSONEach)
		if next.ResultUsage().Type == shape || next.Plan.ResultUsage() != next.ResultUsage() || next.ResultUsage().Type.CompositeMember(0).Usage.Type != fresh[sqltype.Text] || next.ResultUsage().Type.CompositeMember(1).Usage.Type != fresh[valueID] || next.Args[0].ResultUsage().Type != fresh[inputID] || next.SourcePosition() != original.SourcePosition() || next.Properties() != original.Properties() {
			t.Fatal("mixed-generation remap", next)
		}
		cursor, err := exec.OpenFunctionSource(&EvalContext{EvalStatement: evalStatement{Context: context.Background()}}, next)
		if err != nil {
			t.Fatal(err)
		}
		row, present, err := cursor.Next(context.Background())
		cursor.Close()
		if err != nil || !present || row.Type() != next.ResultUsage().Type || row.CompositeField(0).Type() != fresh[sqltype.Text] || row.CompositeField(1).Type() != fresh[valueID] {
			t.Fatal("remapped provider", row, present, err)
		}
		walked := 0
		plan.WalkExpression(original, func(n expr.Expr) {
			if n == original.Args[0] {
				walked++
			}
		})
		if walked != 1 || exec.PlannedVolatility(original) != expr.Immutable || !sameGroupExpression(original, original) {
			t.Fatal("closed traversal", walked)
		}
		clone, err := remapBoundExpression(original, func(r ids.ObjectRef) ids.ObjectRef { return r }, nil)
		if err != nil || !sameGroupExpression(original, clone) {
			t.Fatal("stored reconstruction", clone, err)
		}
		folded, _, err := preflightCoercions(&EvalContext{EvalStatement: evalStatement{Context: context.Background()}}, original)
		if err != nil || !exec.SourceReturnsSet(folded) {
			t.Fatal("preflight folded set", folded, err)
		}
		folded, _, err = simplifyConditional(&EvalContext{EvalStatement: evalStatement{Context: context.Background()}}, original, false)
		if err != nil || !exec.SourceReturnsSet(folded) {
			t.Fatal("simplifier folded set", folded, err)
		}
		text, err := (pgRenderer{view: view}).sql(context.Background(), original, pgRenderParent{})
		if err != nil || !strings.Contains(text, name+"(") || !strings.Contains(text, "::json") {
			t.Fatal("symbolic rendering", text, err)
		}
		other := jsonEachUnitMarker(t, b, name, `{"x":2}`)
		root := &plan.FunctionFromNode{Calls: []expr.Expr{original}, QuerySchema: plan.QuerySchema{{Name: "key", Usage: b.typ(sqltype.Text).Usage()}, {Name: "value", Usage: b.typ(valueID).Usage()}}, Frame: 1}
		rewritten, err := plan.RewriteQueryExpressions(root, func(n expr.Expr) (expr.Expr, bool, error) {
			if n == original.Args[0] {
				return other.Args[0], true, nil
			}
			return n, false, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if rewritten.(*plan.FunctionFromNode).Calls[0].(functions.JSONEach).Args[0] != other.Args[0] || root.Calls[0].(functions.JSONEach).Args[0] != original.Args[0] {
			t.Fatal("mutated retained query")
		}
		if err := exec.ValidateFunctionFromLayout(root); err != nil {
			t.Fatal(err)
		}
		broken := *root
		broken.QuerySchema = plan.QuerySchema{{Usage: b.typ(sqltype.Text).Usage()}, {Usage: b.typ(sqltype.Int4).Usage()}}
		if err := exec.ValidateFunctionFromLayout(&broken); err == nil {
			t.Fatal("invalid leaf span accepted")
		}
	}
}

func TestJSONEachFromPaddingAndTargetSlot(t *testing.T) {
	engine := New()
	b := newTestExpressionBinder(t, context.Background(), engine, sqltype.InputEnvironment{}, nil)
	for _, name := range []string{"json_each", "json_each_text", "jsonb_each", "jsonb_each_text"} {
		marker := jsonEachUnitMarker(t, b, name, `{"a":null}`)
		series, err := b.bindFunction(nullCountRaw("generate_series", parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 7}}, parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 8}}))
		if err != nil {
			t.Fatal(err)
		}
		calls := []expr.Expr{series, marker}
		invocation, err := exec.OpenFunctionInvocation(&EvalContext{EvalStatement: evalStatement{Context: context.Background()}}, &plan.FunctionFromNode{Calls: calls})
		if err != nil {
			t.Fatal(err)
		}
		row := make([]sqltype.Value, 3)
		present, err := invocation.NextFrom(context.Background(), calls, row)
		if err != nil || !present {
			t.Fatal(present, err)
		}
		key, _ := row[1].Text()
		number, _ := row[0].Integer()
		if number != 7 || key != "a" || row[2].IsNull() != strings.HasSuffix(name, "_text") {
			t.Fatal("record presence/null conflated", row)
		}
		present, err = invocation.NextFrom(context.Background(), calls, row)
		if err != nil || !present || !row[1].IsNull() || !row[2].IsNull() || row[1].Type() != b.typ(sqltype.Text) || row[2].Type() != marker.ResultUsage().Type.CompositeMember(1).Usage.Type {
			t.Fatal("incorrect record padding", row, present, err)
		}
		number, _ = row[0].Integer()
		if number != 8 {
			t.Fatal("scalar offset", number)
		}
		present, err = invocation.NextFrom(context.Background(), calls, row)
		invocation.Close()
		if err != nil || present {
			t.Fatal("exhausted source", present, err)
		}
		// The real target stage stores one record slot instead of FROM fields.
		node := regexpSetUnitLayout(&plan.SetProjectNode{QuerySchema: plan.QuerySchema{{Usage: marker.ResultUsage()}}, Child: &plan.SingletonNode{}, Calls: []expr.Expr{marker}, Frame: 1})
		stage, err := exec.OpenSetProjectNode(context.Background(), &queryState{Source: &querySource{}, Frame: 1}, node)
		if err != nil {
			t.Fatal(err)
		}
		target := make([]sqltype.Value, 1)
		present, err = stage.Next(context.Background(), target)
		stage.Close()
		if err != nil || !present || target[0].CompositeLen() != 2 {
			t.Fatal("target flattened", target, present, err)
		}
	}
}
