package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestJSONCreateBindingAndVisitors(t *testing.T) {
	ctx := context.Background()
	engine := New()
	binder := newTestExpressionBinder(t, ctx, engine, sqltype.InputEnvironment{}, nil)
	literal := parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "x"}}
	node, err := binder.bindFunctionProvider(nullCountRaw("json_build_array", literal))
	if err != nil {
		t.Fatal(err)
	}
	bound := node.(functions.JSONCreate)
	if bound.Properties().Volatility != expr.Stable || bound.ResultUsage().Type.ID() != sqltype.JSON || bound.ResultUsage().Typmod != -1 || !bound.UnknownArgument(0) {
		t.Fatal("signature/provenance lost", bound)
	}
	if _, err := binder.bindFunctionProvider(nullCountRaw("to_json", literal)); err == nil {
		t.Fatal("polymorphic unknown string admitted")
	} else {
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "42804" || d.Position != "" {
			t.Fatal(err)
		}
	}
	if plannedVolatility(node) != expr.Stable || generationImmutable(node) || conditionalConstantInputs(node) {
		t.Fatal("STABLE creation treated as immutable")
	}
	visited := 0
	boundExpressionChildren(node, func(expr.Expr) { visited++ })
	if visited != 1 {
		t.Fatal("creation children hidden", visited)
	}
	types := formatUnitTypes()
	fresh, changed, err := retagBoundTypes(binder.bindingCatalog(), node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return types[d.ID()] })
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	reminted := fresh.(functions.JSONCreate)
	if reminted.Args[0].ResultUsage().Type != types[sqltype.Text] || reminted.Usage.Type != types[sqltype.JSON] || bound.Args[0].ResultUsage().Type == reminted.Args[0].ResultUsage().Type {
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

func TestJSONCreateRecordParameterOccurrence(t *testing.T) {
	ctx := context.Background()
	engine := New()
	binder := newTestExpressionBinder(t, ctx, engine, sqltype.InputEnvironment{}, nil)
	raw := nullCountRaw("row_to_json", parser.ParameterExpr{Number: 1})
	first, err := binder.bindFunctionProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	second, err := binder.bindFunctionProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	signature, err := binder.finish()
	if err != nil || signature.Count != 1 || signature.Types[0].Type.ID() != 2249 || !signature.Types[0].Type.IsComposite() {
		t.Fatal("RECORD formal lost occurrence-owned shape", signature, err)
	}
	if first.(functions.JSONCreate).Args[0].ResultUsage().Type != second.(functions.JSONCreate).Args[0].ResultUsage().Type {
		t.Fatal("repeated RECORD formal minted incompatible identity")
	}
	node := first.(functions.JSONCreate)
	eval := &EvalContext{Context: ctx, Parameters: []sqltype.Value{sqltype.Null(signature.Types[0].Type)}}
	got, err := node.Eval(eval)
	if err != nil || !got.IsNull() || got.Type().ID() != sqltype.JSON {
		t.Fatal("RECORD parameter NULL lost", got, err)
	}
	reanalysis := newTestExpressionBinder(t, ctx, engine, sqltype.InputEnvironment{}, nil)
	reanalysis.seedParameters(signature.Types)
	rebound, err := reanalysis.bindFunctionProvider(raw)
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
