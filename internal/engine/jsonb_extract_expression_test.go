package engine

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestJSONBExtractInferenceAndLifecycle(t *testing.T) {
	ctx, engine := context.Background(), New()
	view := engine.CatalogRead()
	for _, tc := range []struct {
		q   string
		ids []sqltype.ID
	}{
		{"SELECT $1::jsonb -> $2", []sqltype.ID{sqltype.JSONB, sqltype.Text}},
		{"SELECT $1::jsonb -> $2::int4", []sqltype.ID{sqltype.JSONB, sqltype.Int4}},
		{"SELECT $1::jsonb #> $2", []sqltype.ID{sqltype.JSONB, 1009}},
		{"SELECT jsonb_extract_path($1,$2,$3)", []sqltype.ID{sqltype.JSONB, sqltype.Text, sqltype.Text}},
		{"SELECT jsonb_extract_path_text($1,VARIADIC $2)", []sqltype.ID{sqltype.JSONB, 1009}},
	} {
		b := newTestExpressionBinder(t, ctx, engine, sqltype.InputEnvironment{}, nil)
		node := jsonbSetUnitBind(t, b, tc.q)
		for i, id := range tc.ids {
			if b.parameters[i+1].Type.ID() != id {
				t.Fatal(tc.q, b.parameters)
			}
		}
		if node.Properties().Volatility != Immutable || !node.Properties().ParameterDependent || plannedVolatility(node) != Immutable {
			t.Fatal(node.Properties())
		}
		count := 0
		boundExpressionChildren(node, func(BoundExpr) { count++ })
		if count != len(tc.ids) {
			t.Fatal("lost children", tc.q, count)
		}
	}
	for _, q := range []string{"SELECT $1 -> 0", "SELECT $1 ->> 'a'", "SELECT $1 #> '{a}'", "SELECT $1 #>> '{a}'"} {
		b := newTestExpressionBinder(t, ctx, engine, sqltype.InputEnvironment{}, nil)
		raw := statement(t, q).(*parser.Select).Targets[0]
		if _, err := b.bind(raw, expressionPolicy{context: projectionContext}); err == nil || !strings.Contains(err.Error(), "operator is not unique") {
			t.Fatal(q, err)
		}
	}
	b := newTestExpressionBinder(t, ctx, engine, sqltype.InputEnvironment{}, nil)
	original := jsonbSetUnitBind(t, b, `SELECT jsonb_extract_path_text(from_json => '{"a":"x"}',VARIADIC path_elems => '{a}'::text[])`).(functions.JSONExtract[BoundExpr, *EvalContext, ExprProperties])
	if err := original.Validate(); err != nil {
		t.Fatal(err)
	}
	fresh := jsonObjectUnitTypes()
	next, changed, err := retagBoundTypes(view, original, func(d *sqltype.Descriptor) *sqltype.Descriptor { return fresh[d.ID()] })
	if err != nil || !changed {
		t.Fatal(next, changed, err)
	}
	n := next.(functions.JSONExtract[BoundExpr, *EvalContext, ExprProperties])
	for i, arg := range n.Args {
		if arg.ResultUsage().Type != fresh[original.Args[i].ResultUsage().Type.ID()] || arg.ResultUsage().Type == original.Args[i].ResultUsage().Type {
			t.Fatal("lost exact owner", i)
		}
	}
	value, err := n.Eval(&EvalContext{Context: ctx})
	if err != nil {
		t.Fatal(err)
	}
	text, err := fresh[sqltype.Text].Output(ctx, value)
	if err != nil || text != "x" {
		t.Fatal(text, err)
	}
	mapped, err := remapBoundExpression(original, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
	if err != nil || !sameGroupExpression(original, mapped) {
		t.Fatal(mapped, err)
	}
	if !n.Variadic || n.Labels[0] != "from_json" || n.Labels[1] != "path_elems" || n.Plan.Name() != "jsonb_extract_path_text" {
		t.Fatal("lost call provenance", n)
	}
	rendered, err := jsonExtractSQL(original, original.Plan.Name(), func(BoundExpr) (string, error) { return "value", nil }, func(s string) string { return s })
	if err != nil || !strings.Contains(rendered, "VARIADIC path_elems => value") {
		t.Fatal(rendered, err)
	}
	copied, did, err := mapJSONExtractChildren(original, func(arg BoundExpr) (BoundExpr, bool, error) { return arg, false, nil })
	if err != nil || did || &copied.Args[0] != &original.Args[0] {
		t.Fatal("unchanged mapper copied", err)
	}
	copied, did, err = mapJSONExtractChildren(original, func(arg BoundExpr) (BoundExpr, bool, error) { return arg, true, nil })
	if err != nil || !did || &copied.Args[0] == &original.Args[0] {
		t.Fatal("changed mapper aliased source", err)
	}
	bad := original
	bad.Variadic = false
	if err := bad.Validate(); err == nil {
		t.Fatal("invalid reconstruction admitted")
	}
}

func TestJSONBExtractStoredVariadicProvenance(t *testing.T) {
	ctx, engine := context.Background(), New()
	view := engine.CatalogRead()
	for _, name := range []string{"jsonb_extract_path", "jsonb_extract_path_text"} {
		for _, named := range []bool{false, true} {
			q := "SELECT " + name + `('{"a":"x"}',VARIADIC '{a}'::text[])`
			if named {
				q = "SELECT " + name + `(from_json => '{"a":"x"}',VARIADIC path_elems => '{a}'::text[])`
			}
			t.Run(q, func(t *testing.T) {
				b := newTestExpressionBinder(t, ctx, engine, sqltype.InputEnvironment{}, nil)
				raw := statement(t, q).(*parser.Select).Targets[0]
				node, err := b.bind(raw, expressionPolicy{context: projectionContext})
				if err != nil {
					t.Fatal(err)
				}
				if err := b.captureViewLiterals(raw, node, viewTargetLiteral, 0); err != nil {
					t.Fatal(err)
				}
				renderer := viewRenderer{viewDefinition: &viewDefinition{literals: b.viewLiterals, castUsages: b.viewCastUsages}, frame: b.frameID()}
				replaySQL, err := renderer.expressionSQL(ctx, view, raw, viewLiteralPath{clause: viewTargetLiteral})
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(replaySQL, "VARIADIC ") || named && !strings.Contains(replaySQL, "path_elems") {
					t.Fatal("lost written provenance", replaySQL)
				}
				replayed := jsonbSetUnitBind(t, newTestExpressionBinder(t, ctx, engine, sqltype.InputEnvironment{}, nil), "SELECT "+replaySQL)
				value, err := replayed.Eval(&EvalContext{Context: ctx})
				if err != nil {
					t.Fatal(err)
				}
				got, err := value.Type().Output(ctx, value)
				want := `"x"`
				if name == "jsonb_extract_path_text" {
					want = "x"
				}
				if err != nil || got != want {
					t.Fatal(replaySQL, got, err)
				}
			})
		}
	}
}
