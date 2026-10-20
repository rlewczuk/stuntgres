package engine

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
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
			if b.Params.Parameters[i+1].Type.ID() != id {
				t.Fatal(tc.q, b.Params.Parameters)
			}
		}
		if node.Properties().Volatility != expr.Immutable || !node.Properties().ParameterDependent || exec.PlannedVolatility(node) != expr.Immutable {
			t.Fatal(node.Properties())
		}
		count := 0
		plan.ExpressionChildren(node, func(expr.Expr) { count++ })
		if count != len(tc.ids) {
			t.Fatal("lost children", tc.q, count)
		}
	}
	for _, q := range []string{"SELECT $1 -> 0", "SELECT $1 ->> 'a'", "SELECT $1 #> '{a}'", "SELECT $1 #>> '{a}'"} {
		b := newTestExpressionBinder(t, ctx, engine, sqltype.InputEnvironment{}, nil)
		raw := statement(t, q).(*parser.Select).Targets[0]
		if _, err := b.Bind(raw, expressionPolicy{Context: analyze.ProjectionContext}); err == nil || !strings.Contains(err.Error(), "operator is not unique") {
			t.Fatal(q, err)
		}
	}
	b := newTestExpressionBinder(t, ctx, engine, sqltype.InputEnvironment{}, nil)
	original := jsonbSetUnitBind(t, b, `SELECT jsonb_extract_path_text(from_json => '{"a":"x"}',VARIADIC path_elems => '{a}'::text[])`).(functions.JSONExtract)
	if err := original.Validate(); err != nil {
		t.Fatal(err)
	}
	fresh := jsonObjectUnitTypes()
	next, changed, err := retagBoundTypes(view, original, func(d *sqltype.Descriptor) *sqltype.Descriptor { return fresh[d.ID()] })
	if err != nil || !changed {
		t.Fatal(next, changed, err)
	}
	n := next.(functions.JSONExtract)
	for i, arg := range n.Args {
		if arg.ResultUsage().Type != fresh[original.Args[i].ResultUsage().Type.ID()] || arg.ResultUsage().Type == original.Args[i].ResultUsage().Type {
			t.Fatal("lost exact owner", i)
		}
	}
	value, err := n.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
	if err != nil {
		t.Fatal(err)
	}
	text, err := fresh[sqltype.Text].Output(ctx, value)
	if err != nil || text != "x" {
		t.Fatal(text, err)
	}
	mapped, err := remapBoundExpression(original, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
	if err != nil || !analyze.SameGroupExpression(original, mapped) {
		t.Fatal(mapped, err)
	}
	if !n.Variadic || n.Labels[0] != "from_json" || n.Labels[1] != "path_elems" || n.Plan.Name() != "jsonb_extract_path_text" {
		t.Fatal("lost call provenance", n)
	}
	spec, err := original.Render()
	if err != nil || spec.Form != expr.FormCall || !spec.Variadic || len(spec.Args) == 0 || spec.Args[len(spec.Args)-1].Label != "path_elems" {
		t.Fatal("replay presentation", spec, err)
	}
	copied, did, err := expr.Map(original, func(arg expr.Expr) (expr.Expr, bool, error) { return arg, false, nil })
	if err != nil || did || &copied.(functions.JSONExtract).Args[0] != &original.Args[0] {
		t.Fatal("unchanged mapper copied", err)
	}
	copied, did, err = expr.Map(original, func(arg expr.Expr) (expr.Expr, bool, error) { return arg, true, nil })
	if err != nil || !did || &copied.(functions.JSONExtract).Args[0] == &original.Args[0] {
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
				node, err := b.Bind(raw, expressionPolicy{Context: analyze.ProjectionContext})
				if err != nil {
					t.Fatal(err)
				}
				if err := b.CaptureViewLiterals(raw, node, analyze.ViewTargetLiteral, 0); err != nil {
					t.Fatal(err)
				}
				renderer := viewRenderer{viewDefinition: &viewDefinition{literals: b.Capture.Literals, castUsages: b.Capture.CastUsages}, frame: b.FrameID()}
				replaySQL, err := renderer.expressionSQL(ctx, view, raw, viewLiteralPath{Clause: analyze.ViewTargetLiteral})
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(replaySQL, "VARIADIC ") || named && !strings.Contains(replaySQL, "path_elems") {
					t.Fatal("lost written provenance", replaySQL)
				}
				replayed := jsonbSetUnitBind(t, newTestExpressionBinder(t, ctx, engine, sqltype.InputEnvironment{}, nil), "SELECT "+replaySQL)
				value, err := replayed.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
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
