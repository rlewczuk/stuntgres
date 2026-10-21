package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestJSONBSetContextualArrayCapture(t *testing.T) {
	t.Run("positional", func(t *testing.T) {
		jsonbSetContextualArrayCapture(t, `SELECT jsonb_set('{}',ARRAY[k::varchar(1)]::text[],'9')`, `{"a": 9}`, `{"b": 9}`)
	})
	t.Run("reordered_lax", func(t *testing.T) {
		jsonbSetContextualArrayCapture(t, `SELECT jsonb_set_lax(null_value_treatment => 'delete_key',replacement => NULL,path => ARRAY[k::varchar(1)]::text[],jsonb_in => '{"a":1,"b":2}')`, `{"b": 2}`, `{"a": 1}`)
	})
}

func jsonbSetContextualArrayCapture(t *testing.T, query, wantA, wantB string) {
	t.Helper()
	ctx := context.Background()
	engine := New()
	b := newTestExpressionBinder(t, ctx, engine, sqltype.InputEnvironment{}, nil)
	column := catalog.Column{Name: "k", Usage: b.Type(sqltype.Text).Usage()}
	b.Query.Namespace = &analyze.QueryNamespace{Columns: []analyze.QueryColumn{{Descriptor: column, Slot: 0, Leaf: -1, Physical: -1}}, Frame: []catalog.Column{column}}
	raw := statement(t, query).(*parser.Select).Targets[0]
	node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil {
		t.Fatal(err)
	}
	// The actual selected-function path must capture the written VARCHAR cast
	// and original column, not mistake the contextual TEXT conversion for it.
	if err := b.CaptureViewLiterals(raw, node, analyze.ViewTargetLiteral, 0); err != nil {
		t.Fatal(err)
	}
	foundColumn, foundTypmod := false, false
	for _, captured := range b.Capture.Columns {
		foundColumn = foundColumn || captured.Frame == b.FrameID() && captured.Slot == 0
	}
	for _, usage := range b.Capture.CastUsages {
		length, known := usage.Length()
		foundTypmod = foundTypmod || usage.Type.ID() == sqltype.VarChar && known && length == 1
	}
	if !foundColumn || !foundTypmod {
		t.Fatal("capture lost original column identity or written typmod", b.Capture.Columns, b.Capture.CastUsages)
	}
	pathConversion := func(node expr.Expr) plan.CoercionExpr {
		e := node.(functions.JSONBSet)
		arg := e.Args[1]
		for {
			wrapper, ok := arg.(plan.CoercionExpr)
			if !ok {
				break
			}
			arg = wrapper.Child
		}
		array := arg.(plan.ArrayExpr)
		conversion := array.Elements[0].(plan.CoercionExpr)
		written := conversion.Child.(plan.CoercionExpr)
		length, known := written.Usage.Length()
		if conversion.Mode != sqltype.Explicit || !conversion.SyntheticConstructorElement || written.SyntheticConstructorElement || written.Mode != sqltype.Explicit || written.Usage.Type.ID() != sqltype.VarChar || !known || length != 1 {
			t.Fatal("conversion context or written origin changed", conversion, written)
		}
		plan, err := sqltype.PlanCoercion(written.ResultUsage(), conversion.ResultUsage(), sqltype.Explicit)
		if err != nil || !plan.Equal(conversion.Plan) {
			t.Fatal("synthetic provenance changed executable plan", err)
		}
		return conversion
	}
	// A row-dependent projection is never a stored default. Independently bind
	// a closed witness with the same written and contextual conversion boundary.
	if _, err := (scalarRenderer{}).sql(ctx, engine.CatalogRead(), node); err == nil {
		t.Fatal("row-dependent projection admitted as a stored default")
	}
	closed := jsonbSetUnitBind(t, newTestExpressionBinder(t, ctx, engine, sqltype.InputEnvironment{}, nil), `SELECT jsonb_set('{}',ARRAY[('ab'::text)::varchar(1)]::text[],'9')`)
	conversion := pathConversion(closed)
	persisted, err := (scalarRenderer{}).sql(ctx, engine.CatalogRead(), conversion)
	if err != nil {
		t.Fatal("explicit plan/default validation changed", err)
	}
	rebound := jsonbSetUnitBind(t, newTestExpressionBinder(t, ctx, engine, sqltype.InputEnvironment{}, nil), "SELECT "+persisted)
	converted, err := rebound.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
	if err != nil {
		t.Fatal(err)
	}
	convertedText, err := converted.Type().Output(ctx, converted)
	if err != nil || convertedText != "a" {
		t.Fatal("stored contextual conversion changed", convertedText, err)
	}
	assertRows := func(node expr.Expr, text *sqltype.Descriptor) {
		for _, tc := range []struct{ input, want string }{{"ab", wantA}, {"bc", wantB}} {
			input := sqltype.String(text, tc.input)
			eval := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: b.FrameID(), Row: []sqltype.Value{input}}}
			got, err := node.Eval(&eval)
			if err != nil {
				t.Fatal(err)
			}
			out, err := got.Type().Output(ctx, got)
			if err != nil || out != tc.want || eval.Row[0] != input {
				t.Fatal("written truncation/contextual conversion lost", out, tc.want, err)
			}
		}
	}
	assertRows(node, b.Type(sqltype.Text))
	mapped, err := remapBoundExpression(node, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
	if err != nil {
		t.Fatal(err)
	}
	pathConversion(mapped)
	assertRows(mapped, b.Type(sqltype.Text))
	fresh := jsonObjectUnitTypes()
	reminted, changed, err := retagBoundTypes(engine.CatalogRead(), node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return fresh[d.ID()] })
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	pathConversion(reminted)
	assertRows(reminted, fresh[sqltype.Text])
	pathConversion(node)
}
