package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestJSONBInsertBindingAndReconstruction(t *testing.T) {
	ctx := context.Background()
	engine := New()
	b := newTestExpressionBinder(t, ctx, engine, sqltype.InputEnvironment{}, nil)
	node := jsonbSetUnitBind(t, b, "SELECT jsonb_insert($1,$2,$3,$4)")
	e := node.(functions.JSONBSet)
	typeIDs := [4]sqltype.ID{sqltype.JSONB, 1009, sqltype.JSONB, sqltype.Bool}
	for i, id := range typeIDs {
		if b.parameters[i+1].Type.ID() != id || e.Args[i].ResultUsage().Type.ID() != id || e.Args[i].ResultUsage().Typmod != -1 {
			t.Fatal("formal inference", i)
		}
		binder := newTestExpressionBinder(t, ctx, engine, sqltype.InputEnvironment{}, nil)
		args := []parser.Expr{datetimeMakeString("[0]"), datetimeMakeString("{0}"), datetimeMakeString("9"), datetimeMakeString("false")}
		args[i] = parser.ParameterExpr{Number: 1}
		n, err := binder.bindFunctionProvider(datetimeMakeRaw("jsonb_insert", args...))
		if err != nil || !n.Properties().ParameterDependent || binder.parameters[1].Type.ID() != id {
			t.Fatal("isolated formal inference", i, err)
		}
	}
	if e.Count != 4 || e.WrittenCount != 4 || !node.Properties().ParameterDependent || node.Properties().Volatility != expr.Immutable || plannedVolatility(node) != expr.Immutable {
		t.Fatal(e, node.Properties())
	}
	for _, tc := range []struct {
		query, want string
		defaultFlag bool
	}{
		{`SELECT jsonb_insert('[0]','{0}','9')`, "[9, 0]", false},
		{`SELECT jsonb_set('[0]','{0}','9')`, "[9]", true},
		{`SELECT jsonb_set_lax('[0]','{0}','9')`, "[9]", true},
	} {
		n := jsonbSetUnitBind(t, b, tc.query).(functions.JSONBSet)
		flag, err := n.Args[3].Eval(&EvalContext{Context: ctx})
		if err != nil {
			t.Fatal(err)
		}
		truth, err := flag.Boolean()
		if err != nil || truth != tc.defaultFlag {
			t.Fatal("wrong mutation default", tc.query, flag, err)
		}
		got, err := n.Eval(&EvalContext{Context: ctx})
		if err != nil {
			t.Fatal(err)
		}
		text, err := got.Type().Output(ctx, got)
		if err != nil || text != tc.want {
			t.Fatal(tc.query, text, err)
		}
	}
	for _, query := range []string{
		`SELECT jsonb_insert(replacement => '9',path => '{0}',jsonb_in => '[0]')`,
		`SELECT jsonb_insert(insert_after => true,replacement => '9',path => '{0}',jsonb_in => '[0]')`,
		`SELECT pg_catalog.jsonb_insert('[0]','{0}',VARIADIC '9'::jsonb)`,
		`SELECT jsonb_insert('[0]','{0}','9',VARIADIC true)`,
	} {
		t.Run(query, func(t *testing.T) {
			n := jsonbSetUnitBind(t, b, query).(functions.JSONBSet)
			fresh := jsonObjectUnitTypes()
			next, changed, err := retagBoundTypes(engine.CatalogRead(), n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return fresh[d.ID()] })
			if err != nil || !changed {
				t.Fatal(next, changed, err)
			}
			r := next.(functions.JSONBSet)
			if r.WrittenSlots != n.WrittenSlots || r.Labels != n.Labels || r.WrittenCount != n.WrittenCount || r.Variadic != n.Variadic || r.Plan.Name() != "jsonb_insert" {
				t.Fatal("presentation lost", r)
			}
			for i, arg := range r.Children() {
				if arg.ResultUsage().Type != fresh[typeIDs[i]] || n.Args[i].ResultUsage().Type == arg.ResultUsage().Type {
					t.Fatal("descriptor remapping", i)
				}
			}
			mapped, err := remapBoundExpression(r, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
			if err != nil || !sameGroupExpression(r, mapped) {
				t.Fatal(mapped, err)
			}
			old, err := n.Eval(&EvalContext{Context: ctx})
			if err != nil {
				t.Fatal(err)
			}
			got, err := mapped.Eval(&EvalContext{Context: ctx})
			if err != nil {
				t.Fatal(err)
			}
			before, err := old.Type().Output(ctx, old)
			if err != nil {
				t.Fatal(err)
			}
			after, err := got.Type().Output(ctx, got)
			if err != nil || before != after || got.Type() != fresh[sqltype.JSONB] {
				t.Fatal(before, after, err)
			}
		})
	}
}

func TestJSONBInsertAllSlotPropertiesAndTraversal(t *testing.T) {
	ctx := context.Background()
	b := newTestExpressionBinder(t, ctx, New(), sqltype.InputEnvironment{}, nil)
	base := jsonbSetUnitBind(t, b, `SELECT jsonb_insert('[0]','{0}','9',false)`).(functions.JSONBSet)
	for slot := range 4 {
		e := base
		column := columnExpression{Info: expr.Info{Usage: e.Args[slot].ResultUsage(), Property: expr.Properties{RowDependent: true}}}
		e.Args[slot] = column
		rewritten, err := b.rewriteGroup(e, []expr.Expr{column})
		if err != nil {
			t.Fatal(slot, err)
		}
		n := rewritten.(functions.JSONBSet)
		if _, ok := n.Args[slot].(groupExpression); !ok {
			t.Fatal("slot not grouped", slot)
		}
		if _, ok := e.Args[slot].(columnExpression); !ok {
			t.Fatal("rewrite mutated source", slot)
		}
		seen := false
		boundExpressionChildren(e, func(child expr.Expr) {
			if sameGroupExpression(child, column) {
				seen = true
			}
		})
		if !seen {
			t.Fatal("dependency child hidden", slot)
		}
		other := e
		other.Args[slot] = base.Args[slot]
		if sameGroupExpression(e, other) {
			t.Fatal("slot ignored in semantic identity", slot)
		}
	}
	binder := newTestExpressionBinder(t, ctx, New(), sqltype.InputEnvironment{}, nil)
	args := []parser.Expr{datetimeMakeString("[0]"), datetimeMakeString("{0}"), datetimeMakeString("9"),
		parser.CastExpr{Child: parser.CastExpr{Child: datetimeMakeRaw("random"), TypeName: parser.TypeName{Name: "text"}}, TypeName: parser.TypeName{Name: "boolean"}}}
	n, err := binder.bindFunctionProvider(datetimeMakeRaw("jsonb_insert", args...))
	if err != nil {
		t.Fatal(err)
	}
	if n.Properties().Volatility != expr.Volatile || plannedVolatility(n) != expr.Volatile || generationImmutable(n) {
		t.Fatal("volatile flag child hidden", n.Properties())
	}
}
