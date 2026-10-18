package engine

import (
	"context"
	"stuntgres/internal/engine/functions"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestJSONArrayLengthEvaluationAndRetag(t *testing.T) {
	types, replacement := map[sqltype.ID]*sqltype.Descriptor{}, map[sqltype.ID]*sqltype.Descriptor{}
	for _, d := range sqltype.Builtins() {
		types[d.ID()] = d
	}
	for _, d := range sqltype.Builtins() {
		replacement[d.ID()] = d
	}
	for _, id := range []sqltype.ID{sqltype.JSON, sqltype.JSONB} {
		name := "json_array_length"
		if id == sqltype.JSONB {
			name = "jsonb_array_length"
		}
		t.Run(name, func(t *testing.T) {
			d := types[id]
			v, err := d.Input(context.Background(), `[[],{},null]`, -1)
			if err != nil {
				t.Fatal(err)
			}
			u := types[sqltype.Int4].Usage()
			p, err := sqltype.BindBinary(name, u, d.Usage())
			if err != nil {
				t.Fatal(err)
			}
			expr := functions.Binary[BoundExpr, ExprContext, ExprProperties]{Info: functionInfo(expressionInfo{usage: u}), Plan: p, Args: []BoundExpr{literalExpression{expressionInfo: expressionInfo{usage: d.Usage()}, value: v}}}
			got, err := expr.Eval(&EvalContext{Context: context.Background()})
			if err != nil {
				t.Fatal(err)
			}
			rebound, changed, err := retagBoundTypes(New().CatalogRead(), expr, func(old *sqltype.Descriptor) *sqltype.Descriptor { return replacement[old.ID()] })
			if err != nil || !changed {
				t.Fatal(changed, err)
			}
			next, ok := rebound.(functions.Binary[BoundExpr, ExprContext, ExprProperties])
			if !ok || next.Plan.Name() != name || next.Plan.ResultUsage().Type != replacement[sqltype.Int4] || next.Args[0].ResultUsage().Type != replacement[id] {
				t.Fatal(rebound)
			}
			result, err := next.Eval(&EvalContext{Context: context.Background()})
			retagged, retagErr := got.Retag(replacement[sqltype.Int4])
			if err != nil || retagErr != nil || result != retagged {
				t.Fatal(result, retagged, err, retagErr)
			}
			length, failure := result.Integer()
			if failure != nil || length != 3 {
				t.Fatal(name, result, failure)
			}
			if expr.Plan.ResultUsage().Type != types[sqltype.Int4] || expr.Args[0].ResultUsage().Type != d {
				t.Fatal("source expression mutated")
			}
		})
	}
}

func TestJSONArrayLengthParameterProperties(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   sqltype.ID
	}{{"json_array_length", sqltype.JSON}, {"jsonb_array_length", sqltype.JSONB}} {
		t.Run(tc.name, func(t *testing.T) {
			b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
			pos := parser.Position{Offset: 7, Character: 8}
			raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: tc.name, Pos: pos}}}, Args: []parser.Expr{parser.ParameterExpr{Number: 1, Pos: parser.Position{Offset: 25, Character: 26}}}, Pos: pos}
			bound, err := b.bind(raw, expressionPolicy{context: projectionContext})
			if err != nil {
				t.Fatal(err)
			}
			expr, ok := bound.(functions.Binary[BoundExpr, ExprContext, ExprProperties])
			if !ok || expr.Plan.Name() != tc.name || bound.ResultUsage() != b.typ(sqltype.Int4).Usage() || b.parameters[1].Type.ID() != tc.id {
				t.Fatal(bound, b.parameters[1])
			}
			if !bound.Properties().ParameterDependent || bound.Properties().Volatility != Immutable {
				t.Fatal(bound.Properties())
			}
		})
	}
}

func TestJSONArrayLengthLiteralDeparse(t *testing.T) {
	engine := New()
	view := engine.CatalogRead()
	b := newTestExpressionBinder(t, context.Background(), engine, sqltype.InputEnvironment{}, nil)
	for _, tc := range []struct {
		id                    sqltype.ID
		name, source, literal string
	}{
		{sqltype.JSON, "json_array_length", ` ["it's",1] `, `' ["it''s",1] '::json`},
		{sqltype.JSONB, "jsonb_array_length", ` ["it's",1] `, `'["it''s", 1]'::jsonb`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			typ := b.typ(tc.id)
			value, err := typ.Input(context.Background(), tc.source, -1)
			if err != nil {
				t.Fatal(err)
			}
			arg := literalExpression{expressionInfo: expressionInfo{usage: typ.Usage()}, value: value}
			renderer := pgRenderer{view: view}
			got, err := renderer.literal(context.Background(), arg)
			if err != nil || got != tc.literal {
				t.Fatalf("literal=%q err=%v want=%q", got, err, tc.literal)
			}
			usage := b.typ(sqltype.Int4).Usage()
			plan, err := sqltype.BindBinary(tc.name, usage, typ.Usage())
			if err != nil {
				t.Fatal(err)
			}
			expr := functions.Binary[BoundExpr, ExprContext, ExprProperties]{Info: functionInfo(expressionInfo{usage: usage}), Plan: plan, Args: []BoundExpr{arg}}
			got, err = renderer.sql(context.Background(), expr, pgRenderParent{})
			want := tc.name + "(" + tc.literal + ")"
			if err != nil || got != want {
				t.Fatalf("call=%q err=%v want=%q", got, err, want)
			}
		})
	}
}
