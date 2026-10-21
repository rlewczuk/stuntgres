package engine

import (
	"context"
	"strings"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func jsonStripUnitBind(t *testing.T, b *analyze.Binder, q string) expr.Expr {
	t.Helper()
	raw := statement(t, q).(*parser.Select).Targets[0]
	node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil {
		t.Fatal(err)
	}
	return node
}

func TestJSONStripNullsBindingInferenceAndProperties(t *testing.T) {
	ctx := context.Background()
	b := newTestExpressionBinder(t, ctx, New(), sqltype.InputEnvironment{}, nil)
	node := jsonStripUnitBind(t, b, "SELECT json_strip_nulls(strip_in_arrays => $2,target => $1)")
	e := node.(functions.JSONStripNulls)
	for i, id := range []sqltype.ID{sqltype.JSON, sqltype.Bool} {
		if b.Params.Parameters[i+1].Type.ID() != id || e.Args[i].ResultUsage().Type.ID() != id {
			t.Fatal("formal inference", i)
		}
	}
	if !node.Properties().ParameterDependent || node.Properties().Volatility != expr.Immutable || stored.PlannedIntrinsicVolatility(node) != expr.Immutable {
		t.Fatal(node.Properties())
	}
	for _, q := range []string{`SELECT json_strip_nulls(NULL,missing)`, `SELECT json_strip_nulls(missing,NULL)`} {
		binder := newTestExpressionBinder(t, ctx, New(), sqltype.InputEnvironment{}, nil)
		if _, err := binder.Bind(statement(t, q).(*parser.Select).Targets[0], analyze.ExpressionPolicy{Context: analyze.ProjectionContext}); err == nil {
			t.Fatal("strict NULL hid binding error", q)
		}
	}
	for _, tc := range []struct {
		q          string
		volatility expr.Volatility
	}{
		{`SELECT json_strip_nulls('{}')`, expr.Immutable}, {`SELECT json_strip_nulls(NULL,NULL)`, expr.Immutable},
	} {
		binder := newTestExpressionBinder(t, ctx, New(), sqltype.InputEnvironment{}, nil)
		n := jsonStripUnitBind(t, binder, tc.q)
		if n.Properties().Volatility != tc.volatility || exec.PlannedVolatility(n) != tc.volatility || stored.PlannedIntrinsicVolatility(n) != expr.Immutable {
			t.Fatal(tc.q, n.Properties(), exec.PlannedVolatility(n))
		}
	}
	// Use the existing typed binder witness for a volatile Boolean child,
	// without depending on unrelated function-argument predicate syntax.
	binder := newTestExpressionBinder(t, ctx, New(), sqltype.InputEnvironment{}, nil)
	randomText := parser.CastExpr{Child: datetimeMakeRaw("random"), TypeName: parser.TypeName{Name: "text"}}
	randomBool := parser.CastExpr{Child: randomText, TypeName: parser.TypeName{Name: "boolean"}}
	volatile, err := binder.BindFunctionProvider(datetimeMakeRaw("json_strip_nulls", datetimeMakeString("{}"), randomBool))
	if err != nil {
		t.Fatal(err)
	}
	if volatile.Properties().Volatility != expr.Volatile || exec.PlannedVolatility(volatile) != expr.Volatile || stored.PlannedIntrinsicVolatility(volatile) != expr.Immutable || plan.GenerationImmutable(volatile) {
		t.Fatal("volatile Boolean child lost", volatile.Properties(), exec.PlannedVolatility(volatile))
	}
}

func TestJSONStripNullsBindingRetagRemap(t *testing.T) {
	ctx := context.Background()
	engine := New()
	view := engine.CatalogRead()
	b := newTestExpressionBinder(t, ctx, engine, sqltype.InputEnvironment{}, nil)
	e := jsonStripUnitBind(t, b, `SELECT json_strip_nulls(strip_in_arrays => true,target => '{"a":null,"b":[null,1]}')`).(functions.JSONStripNulls)
	fresh := jsonObjectUnitTypes()
	next, changed, err := stored.RetagTypes(view, e, func(d *sqltype.Descriptor) *sqltype.Descriptor { return fresh[d.ID()] })
	if err != nil || !changed {
		t.Fatal(next, changed, err)
	}
	n := next.(functions.JSONStripNulls)
	for i, arg := range n.Children() {
		if arg.ResultUsage().Type != fresh[e.Args[i].ResultUsage().Type.ID()] {
			t.Fatal("retag lost authority", i)
		}
	}
	if n.WrittenSlots != e.WrittenSlots || n.Labels != e.Labels || exec.PlannedVolatility(n) != expr.Immutable {
		t.Fatal("retag lost presentation/volatility")
	}
	got, err := n.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := fresh[sqltype.JSON].Output(ctx, got)
	if err != nil || out != `{"b":[1]}` {
		t.Fatal(out, err)
	}
	mapped, err := stored.RemapExpression(e, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
	if err != nil || !analyze.SameGroupExpression(e, mapped) || exec.PlannedVolatility(mapped) != expr.Immutable {
		t.Fatal(mapped, err)
	}
	different := e
	different.Args[1] = plan.LiteralExpr{Info: b.Info(b.Type(sqltype.Bool).Usage(), parser.Position{}, expr.Properties{Volatility: expr.Immutable}), Value: sqltype.Boolean(b.Type(sqltype.Bool), false)}
	if analyze.SameGroupExpression(e, different) {
		t.Fatal("group identity ignored Boolean formal")
	}
	var children []expr.Expr
	plan.ExpressionChildren(e, func(child expr.Expr) { children = append(children, child) })
	if len(children) != 2 || !analyze.SameGroupExpression(children[0], e.Args[0]) || !analyze.SameGroupExpression(children[1], e.Args[1]) {
		t.Fatal("formal traversal", children)
	}
}

func TestJSONStripNullsBindingStoredPresentation(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct{ q, want string }{
		{`SELECT json_strip_nulls(' {"a":null,"b":[null]} ')`, `{"b":[null]}`},
		{`SELECT pg_catalog.json_strip_nulls(strip_in_arrays := true,target := '{"a":null,"b":[null]}')`, `{"b":[]}`},
		{`SELECT json_strip_nulls(target => '{"a":null}',strip_in_arrays => false)`, `{}`},
		{`SELECT json_strip_nulls('{"b":[null]}'::json,VARIADIC true)`, `{"b":[]}`},
		{`SELECT json_strip_nulls(VARIADIC '{"b":[null]}'::json)`, `{"b":[null]}`},
		{`SELECT json_strip_nulls(target => '{"b":[null]}'::json,VARIADIC strip_in_arrays => true)`, `{"b":[]}`},
	} {
		engine := New()
		query := tc.q
		previous := ""
		for round := range 3 {
			b := newTestExpressionBinder(t, ctx, engine, sqltype.InputEnvironment{}, nil)
			raw := statement(t, query).(*parser.Select).Targets[0]
			node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			if err != nil {
				t.Fatal(err)
			}
			value, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
			if err != nil {
				t.Fatal(err)
			}
			text, err := value.Type().Output(ctx, value)
			if err != nil || text != tc.want {
				t.Fatal(text, err)
			}
			if err := b.CaptureViewLiterals(raw, node, analyze.ViewTargetLiteral, 0); err != nil {
				t.Fatal(err)
			}
			renderer := stored.QueryRenderer{View: stored.NewView(stored.ViewParts{Literals: b.Capture.Literals, CastUsages: b.Capture.CastUsages}), Frame: b.FrameID()}
			emitted, err := renderer.ExpressionSQL(ctx, engine.CatalogRead(), raw, analyze.ViewLiteralPath{Clause: analyze.ViewTargetLiteral})
			if err != nil || round > 0 && emitted != previous {
				t.Fatal(emitted, previous, err)
			}
			if strings.Contains(tc.q, "strip_in_arrays") && !strings.Contains(emitted, "strip_in_arrays") {
				t.Fatal("lost label", emitted)
			}
			if strings.Contains(tc.q, "VARIADIC") && !strings.Contains(emitted, "VARIADIC") {
				t.Fatal("lost variadic marker", emitted)
			}
			previous, query = emitted, "SELECT "+emitted
		}
	}
}
