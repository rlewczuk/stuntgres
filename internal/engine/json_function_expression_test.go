package engine

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func jsonFunctionUnitMarker(t *testing.T, d, result *sqltype.Descriptor, input string, offset int) functions.JSONKeys {
	t.Helper()
	v, err := d.Input(context.Background(), input, -1)
	if err != nil {
		t.Fatal(err)
	}
	name := "json_object_keys"
	if d.ID() == sqltype.JSONB {
		name = "jsonb_object_keys"
	}
	plan, err := sqltype.BindJSONKeys(name, d.Usage(), result.Usage())
	if err != nil {
		t.Fatal(err)
	}
	return functions.JSONKeys{Info: expr.Info{Usage: result.Usage(), Pos: parser.Position{Offset: offset, Character: offset + 1}, Property: expr.Properties{StageDependent: true}}, Plan: plan, Args: []expr.Expr{literalExpression{Info: expr.Info{Usage: d.Usage()}, value: v}}}
}

func TestJSONFunctionScalarBindingAndEvaluation(t *testing.T) {
	engine := New()
	b := newTestExpressionBinder(t, context.Background(), engine, sqltype.InputEnvironment{}, nil)
	for _, tc := range []struct {
		name, input, text string
		integer           int64
		id                sqltype.ID
	}{
		{"json_typeof", `null`, "null", 0, sqltype.Text},
		{"jsonb_typeof", `true`, "boolean", 0, sqltype.Text},
		{"json_array_length", `[1,{},null]`, "", 3, sqltype.Int4},
		{"jsonb_array_length", `[]`, "", 0, sqltype.Int4},
	} {
		raw := nullCountRaw(tc.name, parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: tc.input}})
		node, err := b.bindFunction(raw)
		if err != nil {
			t.Fatal(err)
		}
		call, ok := node.(functions.Binary)
		if !ok || call.Plan.Name() != tc.name || node.ResultUsage().Type.ID() != tc.id || node.Properties().Volatility != expr.Immutable || node.Properties().StageDependent {
			t.Fatal(node)
		}
		value, err := node.Eval(&EvalContext{Context: context.Background()})
		if err != nil || value.IsNull() {
			t.Fatal(value, err)
		}
		if tc.id == sqltype.Text {
			text, err := value.Text()
			if err != nil || text != tc.text {
				t.Fatal(text, err)
			}
		} else {
			n, err := value.Integer()
			if err != nil || n != tc.integer {
				t.Fatal(n, err)
			}
		}
		// Dynamic NULL once-only evaluation is a true local child unit;
		// this owner test retains binding/property/result/shape phase assertions.
	}
	// Wrong-kind constants bind successfully; the shape error belongs to evaluation.
	node, err := b.bindFunction(nullCountRaw("json_array_length", parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: `{}`}}))
	if err != nil {
		t.Fatal("eager shape evaluation", err)
	}
	_, err = node.Eval(&EvalContext{Context: context.Background()})
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "22023" || diagnostic.Position != "" {
		t.Fatal(err)
	}
}

func TestJSONFunctionKeysMarkerPropertiesAndPlacement(t *testing.T) {
	engine := New()
	b := newTestExpressionBinder(t, context.Background(), engine, sqltype.InputEnvironment{}, nil)
	b.aggregateClause = "SELECT"
	b.queryNamespace = &queryNamespace{}
	for _, family := range []string{"json", "jsonb"} {
		raw := nullCountRaw(family+"_object_keys", parser.ParameterExpr{Number: 1})
		node, err := b.bindFunction(raw)
		if err != nil {
			t.Fatal(err)
		}
		marker, ok := node.(functions.JSONKeys)
		if !ok || !marker.Properties().StageDependent || !marker.Properties().ParameterDependent || marker.Properties().RowDependent || marker.Properties().Volatility != expr.Immutable || !b.queryHasTargetSet || !isQuerySet(marker) {
			t.Fatal(node)
		}
		// Each family owns its own parameter signature, never infers from a Go value.
		b = newTestExpressionBinder(t, context.Background(), engine, sqltype.InputEnvironment{}, nil)
		b.aggregateClause = "SELECT"
		b.queryNamespace = &queryNamespace{}
	}
	b.aggregateClause = "WHERE"
	_, err := b.bindFunction(nullCountRaw("json_object_keys", parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: `{}`}}))
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != "0A000" || d.Message != "set-returning functions are not allowed in WHERE" || d.Position != "8" {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		raw       parser.FunctionExpr
		signature string
	}{
		{nullCountRaw("json_typeof", parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1}}), "json_typeof(integer)"},
		{nullCountRaw("json_typeof"), "json_typeof()"},
		{nullCountRaw("JSON_TYPEOF", parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}), "JSON_TYPEOF(unknown)"},
	} {
		_, err := b.bindFunction(tc.raw)
		if !errors.As(err, &d) || d.Code != "42883" || d.Message != "function "+tc.signature+" does not exist" {
			t.Fatal(err)
		}
	}
}

func TestJSONFunctionKeysRetagWalkRewriteAndDeparse(t *testing.T) {
	engine := New()
	b := newTestExpressionBinder(t, context.Background(), engine, sqltype.InputEnvironment{}, nil)
	view := engine.CatalogRead()
	original := jsonFunctionUnitMarker(t, b.typ(sqltype.JSON), b.typ(sqltype.Text), `{"b":1,"a":2}`, 20)
	fresh := jsonObjectUnitTypes()
	mapped, changed, err := retagBoundTypes(view, original, func(d *sqltype.Descriptor) *sqltype.Descriptor { return fresh[d.ID()] })
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	next := mapped.(functions.JSONKeys)
	if next.ResultUsage().Type != fresh[sqltype.Text] || next.Args[0].ResultUsage().Type != fresh[sqltype.JSON] || next.Plan.ResultUsage() != next.ResultUsage() || next.SourcePosition() != original.SourcePosition() || next.Properties() != original.Properties() {
		t.Fatal(next)
	}
	cursor, err := openFunctionCall(&EvalContext{Context: context.Background()}, next)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.close()
	value, present, err := cursor.next(context.Background(), fresh[sqltype.Text])
	text, _ := value.Text()
	if err != nil || !present || text != "b" || value.Type() != fresh[sqltype.Text] {
		t.Fatal(value, present, err)
	}
	walked := 0
	walkBoundExpression(original, func(n expr.Expr) {
		if n == original.Args[0] {
			walked++
		}
	})
	if walked != 1 || plannedVolatility(original) != expr.Immutable || !sameGroupExpression(original, original) || querySetDepth(original) != 1 {
		t.Fatal("closed traversal", walked)
	}
	clone, err := remapBoundExpression(original, func(r ids.ObjectRef) ids.ObjectRef { return r }, nil)
	if err != nil || !sameGroupExpression(original, clone) {
		t.Fatal(clone, err)
	}
	folded, _, err := preflightCoercions(&EvalContext{Context: context.Background()}, original)
	if err != nil || !isQuerySet(folded) {
		t.Fatal("preflight folded set", folded, err)
	}
	folded, _, err = simplifyConditional(&EvalContext{Context: context.Background()}, original, false)
	if err != nil || !isQuerySet(folded) {
		t.Fatal("conditional folded set", folded, err)
	}
	text, err = (pgRenderer{view: view}).sql(context.Background(), original, pgRenderParent{})
	if err != nil || !strings.Contains(text, "json_object_keys(") || !strings.Contains(text, "::json") {
		t.Fatal("symbolic rendering", text, err)
	}
	root := &functionFromNode{calls: []expr.Expr{original}, querySchema: querySchema{{Usage: original.ResultUsage()}}, frame: 1}
	other := jsonFunctionUnitMarker(t, b.typ(sqltype.JSON), b.typ(sqltype.Text), `{"x":1}`, 21)
	rewritten, err := rewriteQueryExpressions(root, func(n expr.Expr) (expr.Expr, bool, error) {
		if n == original.Args[0] {
			return other.Args[0], true, nil
		}
		return n, false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if rewritten.(*functionFromNode).calls[0].(functions.JSONKeys).Args[0] != other.Args[0] || root.calls[0].(functions.JSONKeys).Args[0] != original.Args[0] {
		t.Fatal("mutated retained query")
	}
}

func TestJSONFunctionSetZipPaddingEmptyAndChildOnce(t *testing.T) {
	view := New().CatalogRead()
	json, _ := view.LookupType(sqltype.JSON)
	jsonb, _ := view.LookupType(sqltype.JSONB)
	text, _ := view.LookupType(sqltype.Text)
	a := jsonFunctionUnitMarker(t, json, text, `{"b":1,"a":2}`, 10)
	b := jsonFunctionUnitMarker(t, jsonb, text, `{"x":1}`, 20)
	draws := 0
	input := a.Args[0].(literalExpression).value
	a.Args = []expr.Expr{queryProbe(json.Usage(), func(*EvalContext) (sqltype.Value, error) { draws++; return input, nil })}
	n := regexpSetUnitLayout(&setProjectNode{querySchema: querySchema{{Usage: text.Usage()}, {Usage: text.Usage()}}, child: &singletonNode{}, calls: []expr.Expr{a, b}, frame: 1})
	stage, err := openSetProjectNode(context.Background(), &queryState{source: &querySource{materialized: true}, frame: 1}, n)
	if err != nil {
		t.Fatal(err)
	}
	if got := regexpSetUnitRows(t, &stage); !reflect.DeepEqual(got, [][]string{{"b", "x"}, {"a", "<NULL>"}}) || draws != 1 {
		t.Fatal(got, draws)
	}
	empty := jsonFunctionUnitMarker(t, json, text, `{}`, 30)
	empty.Args = []expr.Expr{literalExpression{Info: expr.Info{Usage: json.Usage()}, value: sqltype.Null(json)}}
	n = regexpSetUnitLayout(&setProjectNode{querySchema: querySchema{{Usage: text.Usage()}}, child: &singletonNode{}, calls: []expr.Expr{empty}, frame: 1})
	stage, err = openSetProjectNode(context.Background(), &queryState{source: &querySource{materialized: true}, frame: 1}, n)
	if err != nil {
		t.Fatal(err)
	}
	if got := regexpSetUnitRows(t, &stage); len(got) != 0 {
		t.Fatal("strict singleton NULL set", got)
	}
	failure := errors.New("selected child failure")
	a.Args = []expr.Expr{queryProbe(json.Usage(), func(*EvalContext) (sqltype.Value, error) { return sqltype.Value{}, failure })}
	if cursor, err := openFunctionCall(&EvalContext{Context: context.Background()}, a); !errors.Is(err, failure) || cursor.initialized {
		t.Fatal("published failed invocation", cursor, err)
	}
}
