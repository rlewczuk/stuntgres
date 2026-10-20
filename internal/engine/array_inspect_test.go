package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func arrayInspectUnitRaw(name string, args ...parser.Expr) parser.FunctionExpr {
	return parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Args: args}
}
func arrayInspectUnitArray(input string) parser.Expr {
	return parser.CastExpr{Child: parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: input}}, TypeName: parser.TypeName{Name: "integer", ArrayBounds: []int32{-1}}}
}

func TestArrayInspectResolver(t *testing.T) {
	ctx := context.Background()
	b := newTestExpressionBinder(t, ctx, New(), sqltype.InputEnvironment{}, nil)
	one := parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1}}
	for _, name := range []string{"array_dims", "array_length", "array_lower", "array_upper", "array_ndims", "cardinality"} {
		args := []parser.Expr{arrayInspectUnitArray("{1,NULL}")}
		kind := functions.ArrayInspectFunction(name)
		if kind.Arity() == 2 {
			args = append(args, one)
		}
		node, err := b.bindFunctionProvider(arrayInspectUnitRaw(name, args...))
		if err != nil {
			t.Fatal(name, err)
		}
		call := node.(functions.ArrayInspect)
		if call.Kind != kind || call.ResultUsage().Type != b.typ(kind.Result()) || call.Validate() != nil {
			t.Fatal(call)
		}
		for _, u := range []sqltype.Usage{b.typ(2277).Usage(), {Type: sqltype.LookupOnly(2277, "anyarray", 0), Typmod: -1}} {
			// Exact catalog carrier is admissible; an identical-looking descriptor is not.
			call.Args[0] = columnExpression{Info: b.info(u, parser.Position{}, expr.Properties{RowDependent: true}), slot: 0}
			if u.CatalogNullUsage() {
				if err := call.Validate(); err != nil {
					t.Fatal(err)
				}
				v, err := call.Eval(&EvalContext{evalStatement: evalStatement{Context: ctx}, evalFrame: evalFrame{Row: []sqltype.Value{sqltype.CatalogResultNull(u)}}})
				if err != nil || !v.IsNull() || v.Type() != b.typ(kind.Result()) {
					t.Fatal(v, err)
				}
			} else if call.Validate() == nil {
				t.Fatal("impostor accepted")
			}
		}
	}
	for _, tc := range []struct {
		raw  parser.FunctionExpr
		code string
	}{
		{arrayInspectUnitRaw("array_dims", one), "42883"},
		{arrayInspectUnitRaw("cardinality", parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}), "42804"},
		{arrayInspectUnitRaw("array_length", arrayInspectUnitArray("{1}"), parser.CastExpr{Child: one, TypeName: parser.TypeName{Name: "bigint"}}), "42883"},
	} {
		_, err := b.bindFunctionProvider(tc.raw)
		d, ok := err.(*sqlstate.Error)
		if !ok || d.Code != tc.code {
			t.Fatalf("error=%v want%s", err, tc.code)
		}
	}
}

func TestArrayInspectStoredRemap(t *testing.T) {
	ctx := context.Background()
	source, target := New(), New()
	b := newTestExpressionBinder(t, ctx, source, sqltype.InputEnvironment{}, nil)
	tb := newTestExpressionBinder(t, ctx, target, sqltype.InputEnvironment{}, nil)
	raw := arrayInspectUnitRaw("array_length", arrayInspectUnitArray("[-2:-1][3:5]={{1,NULL,3},{4,5,6}}"), parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 2}})
	node, err := b.bindFunctionProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	old := node.(functions.ArrayInspect)
	mapped, changed, err := retagBoundTypes(target.CatalogRead(), node, func(d *sqltype.Descriptor) *sqltype.Descriptor {
		if d == nil {
			return nil
		}
		return tb.typ(d.ID())
	})
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	next := mapped.(functions.ArrayInspect)
	if err := next.Validate(); err != nil {
		t.Fatal(err)
	}
	if old.Usage.Type != b.typ(sqltype.Int4) || next.Usage.Type != tb.typ(sqltype.Int4) || old.Args[0].ResultUsage().Type == next.Args[0].ResultUsage().Type || old.Args[1].ResultUsage().Type == next.Args[1].ResultUsage().Type {
		t.Fatal("lost generation ownership")
	}
	for _, call := range []expr.Expr{old, next} {
		v, err := call.Eval(&EvalContext{evalStatement: evalStatement{Context: ctx}})
		n, nerr := v.Integer()
		if err != nil || nerr != nil || n != 3 || v.Type() != call.ResultUsage().Type {
			t.Fatal(v, err, nerr)
		}
		if !sameGroupExpression(call, call) || !closedTransactionTimeDefault(call) || !closedUUIDDefault(call) || plannedPhysicalVolatility(call, false) != expr.Immutable {
			t.Fatal("lost scalar traversal")
		}
		visited := 0
		boundExpressionChildren(call, func(expr.Expr) { visited++ })
		if visited != 2 {
			t.Fatal("lost children", visited)
		}
	}
	other := old
	other.Kind = functions.ArrayLower
	if sameGroupExpression(old, other) {
		t.Fatal("different kinds grouped together")
	}
	qualified := old
	qualified.Namespace = "pg_catalog"
	if !sameGroupExpression(old, qualified) || !sameGroupExpression(qualified, old) || old.Namespace != "" {
		t.Fatal("lexical qualification changed resolved grouping identity")
	}
	unchanged, did, err := expr.Map(old, func(child expr.Expr) (expr.Expr, bool, error) { return child, false, nil })
	if err != nil || did || &unchanged.(functions.ArrayInspect).Args[0] != &old.Args[0] {
		t.Fatal("copied unchanged children")
	}
	// A changed child rebuilds on a fresh slice and leaves the source intact.
	changedCall, did, err := expr.Map(old, func(child expr.Expr) (expr.Expr, bool, error) { return child, child == old.Args[1], nil })
	if err != nil || !did || &changedCall.(functions.ArrayInspect).Args[0] == &old.Args[0] || old.Args[1].ResultUsage().Type != b.typ(sqltype.Int4) {
		t.Fatal("child map mutated source", err)
	}
}

func TestArrayInspectClosedDefaults(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	for _, producer := range []string{"clock_timestamp", "uuidv4"} {
		dimension := arrayInspectUnitRaw("num_nonnulls", arrayInspectUnitRaw(producer))
		node, err := b.bindFunctionProvider(arrayInspectUnitRaw("array_length", arrayInspectUnitArray("{1}"), dimension))
		if err != nil {
			t.Fatal(producer, err)
		}
		valid := closedTransactionTimeDefault(node)
		if producer == "uuidv4" {
			valid = closedUUIDDefault(node)
		}
		if !valid || plannedPhysicalVolatility(node, false) != expr.Volatile {
			t.Fatal("lost nested producer", producer)
		}
	}
}
