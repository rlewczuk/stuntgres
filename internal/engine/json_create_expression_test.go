package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestJSONCreateBindingAndVisitors(t *testing.T) {
	ctx := context.Background()
	engine := New()
	binder := newTestExpressionBinder(t, ctx, engine, sqltype.InputEnvironment{}, nil)
	literal := parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "x"}}
	node, err := binder.bindJSONCreate(nullCountRaw("json_build_array", literal))
	if err != nil {
		t.Fatal(err)
	}
	bound := node.(jsonCreateExpression)
	if bound.Properties().Volatility != Stable || bound.ResultUsage().Type.ID() != sqltype.JSON || bound.ResultUsage().Typmod != -1 || !bound.unknownArgument(0) {
		t.Fatal("signature/provenance lost", bound)
	}
	if _, err := binder.bindJSONCreate(nullCountRaw("to_json", literal)); err == nil {
		t.Fatal("polymorphic unknown string admitted")
	} else {
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "42804" || d.Position != "" {
			t.Fatal(err)
		}
	}
	if plannedVolatility(node) != Stable || generationImmutable(node) || conditionalConstantInputs(node) {
		t.Fatal("STABLE creation treated as immutable")
	}
	visited := 0
	boundExpressionChildren(node, func(BoundExpr) { visited++ })
	if visited != 1 {
		t.Fatal("creation children hidden", visited)
	}
	types := formatUnitTypes()
	fresh, changed, err := retagBoundTypes(binder.bindingCatalog(), node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return types[d.ID()] })
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	reminted := fresh.(jsonCreateExpression)
	if reminted.args[0].ResultUsage().Type != types[sqltype.Text] || reminted.usage.Type != types[sqltype.JSON] || bound.args[0].ResultUsage().Type == reminted.args[0].ResultUsage().Type {
		t.Fatal("retag mutated source or lost authority")
	}
	got, err := reminted.Eval(&EvalContext{Context: ctx})
	if err != nil {
		t.Fatal("retag did not rebind creation plan", err)
	}
	text, err := got.JSONText()
	if err != nil || text != `["x"]` {
		t.Fatal(text, err)
	}
	if !sameGroupExpression(node, node) {
		t.Fatal("expression identity not reflexive")
	}
}

type jsonCreateCountingExpression struct {
	expressionInfo
	value   sqltype.Value
	count   *int
	failure error
}

func (e jsonCreateCountingExpression) Eval(*EvalContext) (sqltype.Value, error) {
	*e.count++
	return e.value, e.failure
}
func TestJSONCreateEvaluatesChildrenOnceInOrder(t *testing.T) {
	types := formatUnitTypes()
	one, _ := sqltype.Integer(types[sqltype.Int4], 1)
	counts := [3]int{}
	failure := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22012", Message: "division by zero"}
	args := []BoundExpr{
		jsonCreateCountingExpression{expressionInfo: expressionInfo{usage: one.Type().Usage()}, value: one, count: &counts[0]},
		jsonCreateCountingExpression{expressionInfo: expressionInfo{usage: one.Type().Usage()}, count: &counts[1], failure: failure},
		jsonCreateCountingExpression{expressionInfo: expressionInfo{usage: one.Type().Usage()}, value: one, count: &counts[2]},
	}
	node := jsonCreateExpression{expressionInfo: expressionInfo{usage: types[sqltype.JSON].Usage(), properties: ExprProperties{Volatility: Stable}}, kind: sqltype.JSONBuildObject, args: args}
	if err := node.replan(); err != nil {
		t.Fatal(err)
	}
	_, err := node.Eval(&EvalContext{Context: context.Background()})
	if !errors.Is(err, failure) || counts != [3]int{1, 1, 0} {
		t.Fatal("arity validation hid child evaluation", err, counts)
	}
}

func TestJSONCreateRecordParameterOccurrence(t *testing.T) {
	ctx := context.Background()
	engine := New()
	binder := newTestExpressionBinder(t, ctx, engine, sqltype.InputEnvironment{}, nil)
	raw := nullCountRaw("row_to_json", parser.ParameterExpr{Number: 1})
	first, err := binder.bindJSONCreate(raw)
	if err != nil {
		t.Fatal(err)
	}
	second, err := binder.bindJSONCreate(raw)
	if err != nil {
		t.Fatal(err)
	}
	signature, err := binder.finish()
	if err != nil || signature.Count != 1 || signature.Types[0].Type.ID() != 2249 || !signature.Types[0].Type.IsComposite() {
		t.Fatal("RECORD formal lost occurrence-owned shape", signature, err)
	}
	if first.(jsonCreateExpression).args[0].ResultUsage().Type != second.(jsonCreateExpression).args[0].ResultUsage().Type {
		t.Fatal("repeated RECORD formal minted incompatible identity")
	}
	node := first.(jsonCreateExpression)
	eval := &EvalContext{Context: ctx, Parameters: []sqltype.Value{sqltype.Null(signature.Types[0].Type)}}
	got, err := node.Eval(eval)
	if err != nil || !got.IsNull() || got.Type().ID() != sqltype.JSON {
		t.Fatal("RECORD parameter NULL lost", got, err)
	}
	reanalysis := newTestExpressionBinder(t, ctx, engine, sqltype.InputEnvironment{}, nil)
	reanalysis.seedParameters(signature.Types)
	rebound, err := reanalysis.bindJSONCreate(raw)
	if err != nil {
		t.Fatal("retained RECORD formal lost anonymous shape during reanalysis", err)
	}
	current, err := reanalysis.finish()
	if err != nil || !sameStatementUsage(signature.Types[0], current.Types[0]) || !current.Types[0].Type.IsComposite() {
		t.Fatal("RECORD parameter signature changed", current, err)
	}
	got, err = rebound.Eval(&EvalContext{Context: ctx, Parameters: []sqltype.Value{sqltype.Null(current.Types[0].Type)}})
	if err != nil || !got.IsNull() {
		t.Fatal("reanalysed RECORD NULL failed", got, err)
	}
}
