package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func jsonbSetUnitBind(t *testing.T, b *analyze.Binder, q string) expr.Expr {
	t.Helper()
	raw := statement(t, q).(*parser.Select).Targets[0]
	node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil {
		t.Fatal(q, err)
	}
	return node
}

func TestJSONBSetBindingInferenceAndProperties(t *testing.T) {
	ctx := context.Background()
	b := newTestExpressionBinder(t, ctx, New(), sqltype.InputEnvironment{}, nil)
	node := jsonbSetUnitBind(t, b, "SELECT jsonb_set_lax($1,$2,$3,$4,$5)")
	e := node.(functions.JSONBSet)
	ids := [5]sqltype.ID{sqltype.JSONB, 1009, sqltype.JSONB, sqltype.Bool, sqltype.Text}
	for i, id := range ids {
		if b.Params.Parameters[i+1].Type.ID() != id || e.Args[i].ResultUsage().Type.ID() != id {
			t.Fatal("lost formal inference", i, b.Params.Parameters)
		}
	}
	if e.Count != 5 || e.WrittenCount != 5 || !node.Properties().ParameterDependent || node.Properties().Volatility != expr.Immutable || exec.PlannedVolatility(node) != expr.Immutable {
		t.Fatal(e, node.Properties())
	}
	for slot, id := range ids {
		binder := newTestExpressionBinder(t, ctx, New(), sqltype.InputEnvironment{}, nil)
		args := []parser.Expr{datetimeMakeString("{}"), datetimeMakeString("{a}"), datetimeMakeString("2"), datetimeMakeString("true"), datetimeMakeString("use_json_null")}
		args[slot] = parser.ParameterExpr{Number: 1}
		n, err := binder.BindFunctionProvider(datetimeMakeRaw("jsonb_set_lax", args...))
		if err != nil || !n.Properties().ParameterDependent || binder.Params.Parameters[1].Type.ID() != id {
			t.Fatal("isolated formal property/inference lost", slot, n, err)
		}
	}
	for _, slot := range []int{3, 4} {
		binder := newTestExpressionBinder(t, ctx, New(), sqltype.InputEnvironment{}, nil)
		args := []parser.Expr{datetimeMakeString("{}"), datetimeMakeString("{a}"), datetimeMakeString("2"), datetimeMakeString("true"), datetimeMakeString("use_json_null")}
		randomText := parser.CastExpr{Child: datetimeMakeRaw("random"), TypeName: parser.TypeName{Name: "text"}}
		args[slot] = randomText
		if slot == 3 {
			args[slot] = parser.CastExpr{Child: randomText, TypeName: parser.TypeName{Name: "boolean"}}
		}
		n, err := binder.BindFunctionProvider(datetimeMakeRaw("jsonb_set_lax", args...))
		if err != nil {
			t.Fatal(slot, err)
		}
		if n.Properties().Volatility != expr.Volatile || exec.PlannedVolatility(n) != expr.Volatile || plan.GenerationImmutable(n) {
			t.Fatal("hidden volatile child", slot, n.Properties())
		}
	}
	for _, q := range []string{
		`SELECT jsonb_set_lax(NULL,'{a}',NULL,true,missing)`,
		`SELECT jsonb_set_lax('{}','{a}',NULL,missing,'return_target')`,
	} {
		binder := newTestExpressionBinder(t, ctx, New(), sqltype.InputEnvironment{}, nil)
		_, err := binder.Bind(statement(t, q).(*parser.Select).Targets[0], analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "42703" {
			t.Fatal("ordinary child analysis suppressed", q, err)
		}
	}
}

func TestJSONBSetRetagRemapAndPresentation(t *testing.T) {
	ctx := context.Background()
	engine := New()
	view := engine.CatalogRead()
	b := newTestExpressionBinder(t, ctx, engine, sqltype.InputEnvironment{}, nil)
	node := jsonbSetUnitBind(t, b, `SELECT pg_catalog.jsonb_set_lax(replacement => NULL,jsonb_in => '{"a":1}',path => '{a}',null_value_treatment => 'delete_key')`)
	e := node.(functions.JSONBSet)
	fresh := jsonObjectUnitTypes()
	next, changed, err := retagBoundTypes(view, e, func(d *sqltype.Descriptor) *sqltype.Descriptor { return fresh[d.ID()] })
	if err != nil || !changed {
		t.Fatal(next, changed, err)
	}
	n := next.(functions.JSONBSet)
	for i, arg := range n.Children() {
		if arg.ResultUsage().Type != fresh[e.Args[i].ResultUsage().Type.ID()] || arg.ResultUsage().Type == e.Args[i].ResultUsage().Type {
			t.Fatal("retag lost child authority", i)
		}
	}
	if n.WrittenSlots != e.WrittenSlots || n.Labels != e.Labels || n.WrittenCount != e.WrittenCount {
		t.Fatal("presentation lost")
	}
	got, err := n.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := fresh[sqltype.JSONB].Output(ctx, got)
	if err != nil || out != "{}" {
		t.Fatal(out, err)
	}
	mapped, err := remapBoundExpression(e, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
	if err != nil || !analyze.SameGroupExpression(e, mapped) {
		t.Fatal(mapped, err)
	}
	variadic := jsonbSetUnitBind(t, b, `SELECT pg_catalog.jsonb_set('{"a":1}','{a}',VARIADIC '2'::jsonb)`)
	mapped, err = remapBoundExpression(variadic, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
	if err != nil {
		t.Fatal(err)
	}
	reminted, _, err := retagBoundTypes(view, mapped, func(d *sqltype.Descriptor) *sqltype.Descriptor { return fresh[d.ID()] })
	if err != nil {
		t.Fatal(err)
	}
	v := reminted.(functions.JSONBSet)
	if !v.Variadic {
		t.Fatal("VARIADIC lost")
	}
	var rawArgs []parser.Expr
	for _, text := range []string{"2", `{"a":1}`, "{a}", "delete_key"} {
		rawArgs = append(rawArgs, datetimeMakeString(text))
	}
	raw := datetimeMakeRaw("jsonb_set_lax", rawArgs...)
	raw.Args[0] = parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}
	raw.ArgNames = []parser.Identifier{{Text: "replacement"}, {Text: "jsonb_in"}, {Text: "path"}, {Text: "null_value_treatment"}}
	if err := b.CaptureViewLiterals(raw, e, analyze.ViewTargetLiteral, 0); err != nil {
		t.Fatal("written/formal literal association", err)
	}
	renderer := viewRenderer{viewDefinition: &viewDefinition{literals: b.Capture.Literals, castUsages: b.Capture.CastUsages}, frame: b.FrameID()}
	replaySQL, err := renderer.expressionSQL(ctx, view, raw, analyze.ViewLiteralPath{Clause: analyze.ViewTargetLiteral})
	if err != nil {
		t.Fatal(err)
	}
	replayed := jsonbSetUnitBind(t, newTestExpressionBinder(t, ctx, engine, sqltype.InputEnvironment{}, nil), "SELECT "+replaySQL)
	replayValue, err := replayed.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
	if err != nil {
		t.Fatal("named/default view replay", err)
	}
	replayText, err := replayValue.Type().Output(ctx, replayValue)
	if err != nil || replayText != "{}" {
		t.Fatal("named/default replay changed treatment", replayText, err)
	}
}

func TestJSONBSetViewReplayDeterminism(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct{ q, want string }{
		{`SELECT jsonb_set('{}','{a}','2')`, `{"a": 2}`},
		{`SELECT jsonb_set_lax(jsonb_in => '{"a":1}',path => '{a}',replacement => NULL,null_value_treatment => 'delete_key')`, `{}`},
		{`SELECT pg_catalog.jsonb_set('{}','{a}',VARIADIC '2'::jsonb)`, `{"a": 2}`},
	} {
		t.Run(tc.q, func(t *testing.T) {
			engine := New()
			query := tc.q
			var previous string
			for round := range 3 {
				b := newTestExpressionBinder(t, ctx, engine, sqltype.InputEnvironment{}, nil)
				raw := statement(t, query).(*parser.Select).Targets[0]
				node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
				if err != nil {
					t.Fatal(round, err)
				}
				value, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
				if err != nil {
					t.Fatal(round, err)
				}
				text, err := value.Type().Output(ctx, value)
				if err != nil || text != tc.want {
					t.Fatal("replay changed JSONB result", round, text, err)
				}
				if err := b.CaptureViewLiterals(raw, node, analyze.ViewTargetLiteral, 0); err != nil {
					t.Fatal(round, err)
				}
				renderer := viewRenderer{viewDefinition: &viewDefinition{literals: b.Capture.Literals, castUsages: b.Capture.CastUsages}, frame: b.FrameID()}
				emitted, err := renderer.expressionSQL(ctx, engine.CatalogRead(), raw, analyze.ViewLiteralPath{Clause: analyze.ViewTargetLiteral})
				if err != nil {
					t.Fatal(round, err)
				}
				if round != 0 && emitted != previous {
					t.Fatalf("view replay grew annotations\nbefore:\n%s\nafter:\n%s", previous, emitted)
				}
				previous, query = emitted, "SELECT "+emitted
			}
		})
	}
}

func TestJSONBSetAllSlotGroupAndDependencyTraversal(t *testing.T) {
	ctx := context.Background()
	b := newTestExpressionBinder(t, ctx, New(), sqltype.InputEnvironment{}, nil)
	base := jsonbSetUnitBind(t, b, `SELECT jsonb_set_lax('{"a":1}','{a}',NULL,true,'return_target')`).(functions.JSONBSet)
	for slot := range 5 {
		e := base
		column := plan.ColumnExpr{Info: expr.Info{Usage: e.Args[slot].ResultUsage(), Property: expr.Properties{RowDependent: true}}}
		e.Args[slot] = column
		rewritten, err := b.RewriteGroup(e, []expr.Expr{column})
		if err != nil {
			t.Fatal(slot, err)
		}
		n := rewritten.(functions.JSONBSet)
		if _, ok := n.Args[slot].(plan.GroupExpr); !ok {
			t.Fatal("slot not grouped", slot, n.Args[slot])
		}
		if _, ok := e.Args[slot].(plan.ColumnExpr); !ok {
			t.Fatal("rewrite mutated original", slot)
		}
		seen := false
		plan.ExpressionChildren(e, func(child expr.Expr) {
			if analyze.SameGroupExpression(child, column) {
				seen = true
			}
		})
		if !seen {
			t.Fatal("dependency child hidden", slot)
		}
		other := e
		other.Args[slot] = base.Args[slot]
		if analyze.SameGroupExpression(e, other) {
			t.Fatal("slot ignored in semantic identity", slot)
		}
	}
}

func TestJSONBSetConstantPreflight(t *testing.T) {
	ctx := context.Background()
	eval := &exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}
	for _, q := range []string{`SELECT jsonb_set('{"a":1}','{a}','2')`, `SELECT jsonb_set_lax('{"a":1}','{a}',NULL,true,'delete_key')`} {
		b := newTestExpressionBinder(t, ctx, New(), sqltype.InputEnvironment{}, nil)
		node := jsonbSetUnitBind(t, b, q)
		next, changed, err := preflightCoercionsWithPolicy(eval, node, true)
		if err != nil || !changed {
			t.Fatal(next, changed, err)
		}
		if _, ok := next.(plan.LiteralExpr); !ok {
			t.Fatal("immutable replacement not folded", next)
		}
	}
	b := newTestExpressionBinder(t, ctx, New(), sqltype.InputEnvironment{}, nil)
	node := jsonbSetUnitBind(t, b, `SELECT jsonb_set_lax('{"a":1}','{a}','2',true,NULL)`)
	_, _, err := preflightCoercionsWithPolicy(eval, node, true)
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != "22023" {
		t.Fatal("NULL mode incorrectly strict-folded", err)
	}
}
