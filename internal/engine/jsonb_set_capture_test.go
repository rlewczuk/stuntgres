package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
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
	column := catalog.Column{Name: "k", Usage: b.typ(sqltype.Text).Usage()}
	b.query.namespace = &queryNamespace{columns: []queryColumn{{descriptor: column, slot: 0, leaf: -1, physical: -1}}, frame: []catalog.Column{column}}
	raw := statement(t, query).(*parser.Select).Targets[0]
	node, err := b.bind(raw, expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal(err)
	}
	// The actual selected-function path must capture the written VARCHAR cast
	// and original column, not mistake the contextual TEXT conversion for it.
	if err := b.captureViewLiterals(raw, node, viewTargetLiteral, 0); err != nil {
		t.Fatal(err)
	}
	foundColumn, foundTypmod := false, false
	for _, captured := range b.capture.columns {
		foundColumn = foundColumn || captured.frame == b.frameID() && captured.slot == 0
	}
	for _, usage := range b.capture.castUsages {
		length, known := usage.Length()
		foundTypmod = foundTypmod || usage.Type.ID() == sqltype.VarChar && known && length == 1
	}
	if !foundColumn || !foundTypmod {
		t.Fatal("capture lost original column identity or written typmod", b.capture.columns, b.capture.castUsages)
	}
	pathConversion := func(node expr.Expr) coercionExpression {
		e := node.(functions.JSONBSet)
		arg := e.Args[1]
		for {
			wrapper, ok := arg.(coercionExpression)
			if !ok {
				break
			}
			arg = wrapper.Child
		}
		array := arg.(arrayExpression)
		conversion := array.Elements[0].(coercionExpression)
		written := conversion.Child.(coercionExpression)
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
	stored, err := (scalarRenderer{}).sql(ctx, engine.CatalogRead(), conversion)
	if err != nil {
		t.Fatal("explicit plan/default validation changed", err)
	}
	rebound := jsonbSetUnitBind(t, newTestExpressionBinder(t, ctx, engine, sqltype.InputEnvironment{}, nil), "SELECT "+stored)
	converted, err := rebound.Eval(&EvalContext{evalStatement: evalStatement{Context: ctx}})
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
			eval := EvalContext{evalStatement: evalStatement{Context: ctx}, evalFrame: evalFrame{Scope: 1, Frame: b.frameID(), Row: []sqltype.Value{input}}}
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
	assertRows(node, b.typ(sqltype.Text))
	mapped, err := remapBoundExpression(node, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
	if err != nil {
		t.Fatal(err)
	}
	pathConversion(mapped)
	assertRows(mapped, b.typ(sqltype.Text))
	fresh := jsonObjectUnitTypes()
	reminted, changed, err := retagBoundTypes(engine.CatalogRead(), node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return fresh[d.ID()] })
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	pathConversion(reminted)
	assertRows(reminted, fresh[sqltype.Text])
	pathConversion(node)
}
