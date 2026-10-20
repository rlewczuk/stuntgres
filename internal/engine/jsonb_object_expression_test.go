package engine

import (
	"context"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestJSONBObjectExpressionRetagRemapAndRendering(t *testing.T) {
	types, replacement := jsonObjectUnitTypes(), jsonObjectUnitTypes()
	engine := New()
	view := engine.CatalogRead()
	binder := newTestExpressionBinder(t, context.Background(), engine, sqltype.InputEnvironment{}, nil)
	for id := range types {
		types[id] = binder.typ(id)
	}
	for _, texts := range [][]string{{"[-2:1]={a,1,b,2}"}, {"[-2:-1]={a,b}", "[7:8]={1,2}"}} {
		args := make([]expr.Expr, len(texts))
		usages := make([]sqltype.Usage, len(texts))
		for i, text := range texts {
			value, err := types[1009].Input(context.Background(), text, -1)
			if err != nil {
				t.Fatal(err)
			}
			usages[i] = types[1009].Usage()
			args[i] = literalExpression{Info: expr.Info{Usage: usages[i]}, value: value}
		}
		plan, err := sqltype.BindBinary("jsonb_object", types[sqltype.JSONB].Usage(), usages...)
		if err != nil {
			t.Fatal(err)
		}
		original := functions.Binary{Info: expr.Info{Usage: types[sqltype.JSONB].Usage()}, Plan: plan, Args: args}
		rebound, changed, err := retagBoundTypes(view, original, func(d *sqltype.Descriptor) *sqltype.Descriptor { return replacement[d.ID()] })
		if err != nil || !changed {
			t.Fatal(changed, err)
		}
		next := rebound.(functions.Binary)
		if next.Plan.ResultUsage().Type != replacement[sqltype.JSONB] || next.Args[0].ResultUsage().Type != replacement[1009] {
			t.Fatal(next)
		}
		got, err := next.Eval(&EvalContext{evalStatement: evalStatement{Context: context.Background()}})
		if err != nil {
			t.Fatal(err)
		}
		text, err := next.Plan.ResultUsage().Type.Output(context.Background(), got)
		if err != nil || text != `{"a": "1", "b": "2"}` {
			t.Fatal(text, err)
		}
		mapped, err := remapBoundExpression(original, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
		if err != nil || !sameGroupExpression(original, mapped) {
			t.Fatal(mapped, err)
		}
		if !sameGroupExpression(original, original) {
			t.Fatal("lost expression identity")
		}
		persistent, err := (scalarRenderer{}).scalarFunctionSQL(context.Background(), view, original)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(persistent, "pg_catalog.jsonb_object(") {
			t.Fatal("lost symbolic call", persistent)
		}
		public, err := (pgRenderer{view: view}).sql(context.Background(), original, pgRenderParent{})
		if err != nil {
			t.Fatal(err)
		}
		want := `jsonb_object('[-2:1]={a,1,b,2}'::text[])`
		if len(texts) == 2 {
			want = `jsonb_object('[-2:-1]={a,b}'::text[], '[7:8]={1,2}'::text[])`
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
			raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "pg_catalog"}, {Text: "jsonb_object"}}}, Args: rawArgs}
			if err := restoreScalar(raw, false); err == nil {
				t.Fatal("Restore admitted parameter", slot)
			}
		}
	}
}

func TestJSONBObjectExpressionGroupTraversal(t *testing.T) {
	types := jsonObjectUnitTypes()
	usage := types[1009].Usage()
	column := columnExpression{Info: expr.Info{Usage: usage, Property: expr.Properties{RowDependent: true}}}
	plan, err := sqltype.BindBinary("jsonb_object", types[sqltype.JSONB].Usage(), usage, usage)
	if err != nil {
		t.Fatal(err)
	}
	bound := functions.Binary{Info: expr.Info{Usage: types[sqltype.JSONB].Usage()}, Plan: plan, Args: []expr.Expr{column, column}}
	binder := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	rewritten, err := binder.rewriteGroup(bound, []expr.Expr{column})
	if err != nil {
		t.Fatal(err)
	}
	next := rewritten.(functions.Binary)
	for _, arg := range next.Args {
		if _, ok := arg.(groupExpression); !ok {
			t.Fatal(arg)
		}
	}
	if _, ok := bound.Args[0].(columnExpression); !ok {
		t.Fatal("mutated original arguments")
	}
	var count int
	boundExpressionChildren(bound, func(child expr.Expr) {
		if !sameGroupExpression(child, column) {
			t.Fatal(child)
		}
		count++
	})
	if count != 2 {
		t.Fatal(count)
	}
}

func TestJSONBObjectBinderSignatureBoundaries(t *testing.T) {
	binder := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	pos := parser.Position{Offset: 14, Character: 14}
	unknown := parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "{a,1}", Pos: pos}}
	for _, tc := range []struct {
		name string
		args []parser.Expr
	}{
		{"jsonb_object", []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1, Pos: pos}}}},
		{"jsonb_object", []parser.Expr{unknown, unknown, unknown}},
	} {
		raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "pg_catalog", Pos: pos}, {Text: tc.name, Pos: pos}}}, Args: tc.args, Pos: pos}
		if _, err := binder.bindFunctionProvider(raw); err == nil {
			t.Fatal("admitted incompatible or adjacent signature", tc.name)
		}
	}
	for _, variadic := range []bool{false, true} {
		raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "pg_catalog", Pos: pos}, {Text: "jsonb_object", Pos: pos}}}, Args: []parser.Expr{unknown}, Variadic: variadic, Pos: pos}
		node, err := binder.bindFunctionProvider(raw)
		if err != nil {
			t.Fatal(err)
		}
		call, ok := node.(functions.Binary)
		if !ok || len(call.Args) != 1 || call.Args[0].ResultUsage().Type.ID() != 1009 || node.ResultUsage().Type.ID() != sqltype.JSONB || node.SourcePosition() != pos {
			t.Fatal(node)
		}
	}
}

func TestJSONBObjectBinderInferenceAndProperties(t *testing.T) {
	for _, query := range []string{"SELECT jsonb_object($1)", "SELECT jsonb_object($1,$2)", "SELECT jsonb_object($1,$1)"} {
		b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
		raw := statement(t, query).(*parser.Select).Targets[0]
		node, err := b.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(query, err)
		}
		call := node.(functions.Binary)
		if call.Plan.Name() != "jsonb_object" || node.ResultUsage().Type.ID() != sqltype.JSONB || !node.Properties().ParameterDependent || node.Properties().Volatility != expr.Immutable {
			t.Fatal(query, node.Properties())
		}
		for _, usage := range b.params.parameters {
			if usage.Type.ID() != 1009 || usage.Typmod != -1 {
				t.Fatal("lost TEXT[] inference", usage)
			}
		}
	}
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	array := b.typ(1009).Usage()
	b.target.relation = &catalog.Relation{
		Columns:    []catalog.Column{{Name: "a", Usage: array}, {Name: "v", Usage: array}},
		Attributes: []int32{1, 2}, Index: map[string]int{"a": 0, "v": 1},
	}
	node, err := b.bind(statement(t, "SELECT jsonb_object(a,v)").(*parser.Select).Targets[0], expressionPolicy{context: projectionContext})
	if err != nil || !node.Properties().RowDependent || node.Properties().ParameterDependent || node.Properties().Volatility != expr.Immutable {
		t.Fatal(node, err)
	}
}

func TestJSONBObjectBinderStageAndVolatility(t *testing.T) {
	for _, tc := range []struct {
		query string
		want  expr.Properties
	}{
		{"SELECT jsonb_object(array_agg($1::text))", expr.Properties{StageDependent: true, ParameterDependent: true}},
		{"SELECT jsonb_object(ARRAY['k',current_date::text])", expr.Properties{Volatility: expr.Stable}},
		{"SELECT jsonb_object(ARRAY['k',clock_timestamp()::text])", expr.Properties{Volatility: expr.Volatile}},
	} {
		b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
		b.clause.aggregateClause = "SELECT"
		raw := statement(t, tc.query).(*parser.Select).Targets[0]
		node, err := b.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(tc.query, err)
		}
		if node.Properties() != tc.want {
			t.Fatal(tc.query, node.Properties(), tc.want)
		}
	}
}
