package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
	"testing"
)

// Public persisted rows/replay belong to reference. This directly exercises the
// actual graph remapper's descriptor-owned support and immutable original graph.
func TestQueryWindowRangeDescriptorSupportRemap(t *testing.T) {
	_, query, _ := queryCoreFixture(t, "SELECT count(*) OVER w FROM t WINDOW w AS(ORDER BY id RANGE BETWEEN 1 PRECEDING AND 1 FOLLOWING)")
	original := query.Root.(*plan.ProjectNode).Child.(*plan.WindowNode)
	before := original.Specs[0].Frame
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	mapped, err := remapViewQuery(New().CatalogRead(), query.Root, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(old *sqltype.Descriptor) *sqltype.Descriptor { return types[old.ID()] })
	if err != nil || mapped == query.Root {
		t.Fatal(mapped, err)
	}
	next := mapped.(*plan.ProjectNode).Child.(*plan.WindowNode)
	if next == original || original.Specs[0].Definition != 1 || next.Specs[0].Definition != original.Specs[0].Definition || !reflect.DeepEqual(next.SortIndices, original.SortIndices) || next.Calls[0].Expression == original.Calls[0].Expression || next.Calls[0].Expression.Slot != original.Calls[0].Expression.Slot {
		t.Fatal("graph/slot ownership", next, original)
	}
	ctx := context.Background()
	evaluation := EvalContext{evalStatement: evalStatement{Context: ctx}}
	for _, pair := range [][2]plan.WindowBound{{before.Start, next.Specs[0].Frame.Start}, {before.End, next.Specs[0].Frame.End}} {
		old, bound := pair[0], pair[1]
		if bound.Kind != old.Kind || bound.Offset == old.Offset || bound.Support == old.Support || bound.Offset.ResultUsage().Type != types[sqltype.Int4] || old.Offset.ResultUsage().Type == types[sqltype.Int4] {
			t.Fatal("support/offset ownership", old, bound)
		}
		offset, err := bound.Offset.Eval(&evaluation)
		if err != nil {
			t.Fatal(err)
		}
		val, _ := sqltype.Integer(types[sqltype.Int4], 3)
		base, _ := sqltype.Integer(types[sqltype.Int4], 2)
		got, err := bound.Support.InRange(ctx, val, base, offset, true, false, sqltype.InputEnvironment{})
		if err != nil || !got {
			t.Fatal(got, err)
		}
		if _, err := old.Support.InRange(ctx, val, base, offset, true, false, sqltype.InputEnvironment{}); err == nil {
			t.Fatal("original support accepted replacement authority")
		}
	}
	if original.Specs[0].Frame != before {
		t.Fatal("remap mutated original frame")
	}
}

func TestQueryWindowRangeNamedFrameCloneIdentity(t *testing.T) {
	_, query, _ := queryCoreFixture(t, "SELECT count(*) OVER w,count(*) OVER w,count(*) OVER v FROM t WINDOW w AS(ORDER BY id RANGE 1 PRECEDING),v AS(ORDER BY id RANGE 1 PRECEDING)")
	project := query.Root.(*plan.ProjectNode)
	original := project.Child.(*plan.WindowNode)
	if len(original.Specs) != 2 || len(original.Calls) != 2 || original.Specs[0].Definition != 1 || original.Specs[1].Definition != 2 || !reflect.DeepEqual(original.SortIndices, []int{0, 0}) || !reflect.DeepEqual(original.SpecIndices, []int{0, 1}) {
		t.Fatal("named frame identity or shared sort", original)
	}
	a, b, c := project.Expressions[0].(*plan.WindowExpr), project.Expressions[1].(*plan.WindowExpr), project.Expressions[2].(*plan.WindowExpr)
	if a.Slot != b.Slot || a.Slot == c.Slot {
		t.Fatal("same-name/distinct-name call slots", a, b, c)
	}
	copied, err := rewriteQueryExpressions(query.Root, func(expr expr.Expr) (expr.Expr, bool, error) {
		literal, ok := expr.(plan.LiteralExpr)
		if !ok {
			return expr, false, nil
		}
		value, err := sqltype.Integer(literal.ResultUsage().Type, 2)
		literal.Value = value
		return literal, true, err
	})
	if err != nil || copied == query.Root {
		t.Fatal(copied, err)
	}
	next := copied.(*plan.ProjectNode).Child.(*plan.WindowNode)
	if next == original || !reflect.DeepEqual(next.SortIndices, original.SortIndices) {
		t.Fatal("clone/sort ownership", next)
	}
	evaluation := EvalContext{evalStatement: evalStatement{Context: context.Background()}}
	for i, spec := range original.Specs {
		if next.Specs[i].Definition != spec.Definition {
			t.Fatal("rewrite lost named identity")
		}
		oldValue, err := spec.Frame.Start.Offset.Eval(&evaluation)
		if err != nil {
			t.Fatal(err)
		}
		newValue, err := next.Specs[i].Frame.Start.Offset.Eval(&evaluation)
		if err != nil {
			t.Fatal(err)
		}
		oldInteger, _ := oldValue.Integer()
		newInteger, _ := newValue.Integer()
		if oldInteger != 1 || newInteger != 2 {
			t.Fatal("rewrite mutated original offset", oldValue, newValue)
		}
	}
}
