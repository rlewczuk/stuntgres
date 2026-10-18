package engine

import (
	"context"
	"strings"
	"stuntgres/internal/engine/functions"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func jsonObjectUnitTypes() map[sqltype.ID]*sqltype.Descriptor {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, d := range sqltype.Builtins() {
		types[d.ID()] = d
	}
	return types
}

func TestJSONObjectExpressionRetagRemapAndRendering(t *testing.T) {
	types, replacement := jsonObjectUnitTypes(), jsonObjectUnitTypes()
	engine := New()
	view := engine.CatalogRead()
	binder := newTestExpressionBinder(t, context.Background(), engine, sqltype.InputEnvironment{}, nil)
	for id := range types {
		types[id] = binder.typ(id)
	}
	for _, texts := range [][]string{{"[-2:1]={a,1,b,2}"}, {"[-2:-1]={a,b}", "[7:8]={1,2}"}} {
		args := make([]BoundExpr, len(texts))
		usages := make([]sqltype.Usage, len(texts))
		for i, text := range texts {
			value, err := types[1009].Input(context.Background(), text, -1)
			if err != nil {
				t.Fatal(err)
			}
			usages[i] = types[1009].Usage()
			args[i] = literalExpression{expressionInfo: expressionInfo{Usage: usages[i]}, value: value}
		}
		plan, err := sqltype.BindBinary("json_object", types[sqltype.JSON].Usage(), usages...)
		if err != nil {
			t.Fatal(err)
		}
		original := functions.Binary{Info: functionInfo(expressionInfo{Usage: types[sqltype.JSON].Usage()}), Plan: plan, Args: args}
		rebound, changed, err := retagBoundTypes(view, original, func(d *sqltype.Descriptor) *sqltype.Descriptor { return replacement[d.ID()] })
		if err != nil || !changed {
			t.Fatal(changed, err)
		}
		next := rebound.(functions.Binary)
		if next.Plan.ResultUsage().Type != replacement[sqltype.JSON] || next.Args[0].ResultUsage().Type != replacement[1009] {
			t.Fatal(next)
		}
		got, err := next.Eval(&EvalContext{Context: context.Background()})
		if err != nil {
			t.Fatal(err)
		}
		text, err := got.JSONText()
		if err != nil || text != `{"a" : "1", "b" : "2"}` {
			t.Fatal(text, err)
		}
		mapped, err := remapBoundExpression(original, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
		if err != nil || !sameGroupExpression(original, mapped) {
			t.Fatal(mapped, err)
		}
		if !sameGroupExpression(original, original) {
			t.Fatal("lost expression identity")
		}
		persistent, err := (scalarRenderer{}).scalarFunctionSQL(context.Background(), view, original, original.Usage)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(persistent, "pg_catalog.json_object(") {
			t.Fatal("lost symbolic call", persistent)
		}
		public, err := (pgRenderer{view: view}).sql(context.Background(), original, pgRenderParent{})
		if err != nil {
			t.Fatal(err)
		}
		want := `"json_object"('[-2:1]={a,1,b,2}'::text[])`
		if len(texts) == 2 {
			want = `"json_object"('[-2:-1]={a,b}'::text[], '[7:8]={1,2}'::text[])`
		}
		if public != want {
			t.Fatal(public, want)
		}
		for slot := range texts {
			rawArgs := make([]parser.Expr, len(texts))
			for i := range rawArgs {
				rawArgs[i] = parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}
			}
			rawArgs[slot] = parser.ParameterExpr{Number: 1}
			raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "pg_catalog"}, {Text: "json_object"}}}, Args: rawArgs}
			if err := restoreScalar(raw, false); err == nil {
				t.Fatal("Restore admitted parameter", slot)
			}
		}
	}
}

func TestJSONObjectExpressionGroupTraversal(t *testing.T) {
	types := jsonObjectUnitTypes()
	usage := types[1009].Usage()
	column := columnExpression{expressionInfo: expressionInfo{Usage: usage, Property: ExprProperties{RowDependent: true}}}
	plan, err := sqltype.BindBinary("json_object", types[sqltype.JSON].Usage(), usage, usage)
	if err != nil {
		t.Fatal(err)
	}
	expr := functions.Binary{Info: functionInfo(expressionInfo{Usage: types[sqltype.JSON].Usage()}), Plan: plan, Args: []BoundExpr{column, column}}
	binder := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	rewritten, err := binder.rewriteGroup(expr, []BoundExpr{column})
	if err != nil {
		t.Fatal(err)
	}
	next := rewritten.(functions.Binary)
	for _, arg := range next.Args {
		if _, ok := arg.(groupExpression); !ok {
			t.Fatal(arg)
		}
	}
	if _, ok := expr.Args[0].(columnExpression); !ok {
		t.Fatal("mutated original arguments")
	}
	var count int
	boundExpressionChildren(expr, func(child BoundExpr) {
		if !sameGroupExpression(child, column) {
			t.Fatal(child)
		}
		count++
	})
	if count != 2 {
		t.Fatal(count)
	}
}

func TestJSONObjectBinderSignatureBoundaries(t *testing.T) {
	binder := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	pos := parser.Position{Offset: 14, Character: 14}
	unknown := parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "{a,1}", Pos: pos}}
	for _, tc := range []struct {
		name string
		args []parser.Expr
	}{
		{"json_object", []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1, Pos: pos}}}},
		{"json_object", []parser.Expr{unknown, unknown, unknown}},
	} {
		raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "pg_catalog", Pos: pos}, {Text: tc.name, Pos: pos}}}, Args: tc.args, Pos: pos}
		if _, err := binder.bindFunctionProvider(raw); err == nil {
			t.Fatal("admitted incompatible or adjacent signature", tc.name)
		}
	}
	for _, variadic := range []bool{false, true} {
		raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "pg_catalog", Pos: pos}, {Text: "json_object", Pos: pos}}}, Args: []parser.Expr{unknown}, Variadic: variadic, Pos: pos}
		node, err := binder.bindFunctionProvider(raw)
		if err != nil {
			t.Fatal(err)
		}
		call, ok := node.(functions.Binary)
		if !ok || len(call.Args) != 1 || call.Args[0].ResultUsage().Type.ID() != 1009 || node.ResultUsage().Type.ID() != sqltype.JSON || node.SourcePosition() != pos {
			t.Fatal(node)
		}
	}
}
