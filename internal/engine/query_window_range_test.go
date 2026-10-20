package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

// Public persisted rows/replay belong to reference. This directly exercises the
// actual graph remapper's descriptor-owned support and immutable original graph.
func TestQueryWindowRangeDescriptorSupportRemap(t *testing.T) {
	_, query, _ := queryCoreFixture(t, "SELECT count(*) OVER w FROM t WINDOW w AS(ORDER BY id RANGE BETWEEN 1 PRECEDING AND 1 FOLLOWING)")
	original := query.Root.(*projectNode).child.(*windowNode)
	before := original.specs[0].frame
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	mapped, err := remapViewQuery(New().CatalogRead(), query.Root, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(old *sqltype.Descriptor) *sqltype.Descriptor { return types[old.ID()] })
	if err != nil || mapped == query.Root {
		t.Fatal(mapped, err)
	}
	next := mapped.(*projectNode).child.(*windowNode)
	if next == original || original.specs[0].definition != 1 || next.specs[0].definition != original.specs[0].definition || !reflect.DeepEqual(next.sortIndices, original.sortIndices) || next.calls[0].expression == original.calls[0].expression || next.calls[0].expression.slot != original.calls[0].expression.slot {
		t.Fatal("graph/slot ownership", next, original)
	}
	ctx := context.Background()
	evaluation := EvalContext{evalStatement: evalStatement{Context: ctx}}
	for _, pair := range [][2]boundWindowBound{{before.start, next.specs[0].frame.start}, {before.end, next.specs[0].frame.end}} {
		old, bound := pair[0], pair[1]
		if bound.kind != old.kind || bound.offset == old.offset || bound.support == old.support || bound.offset.ResultUsage().Type != types[sqltype.Int4] || old.offset.ResultUsage().Type == types[sqltype.Int4] {
			t.Fatal("support/offset ownership", old, bound)
		}
		offset, err := bound.offset.Eval(&evaluation)
		if err != nil {
			t.Fatal(err)
		}
		val, _ := sqltype.Integer(types[sqltype.Int4], 3)
		base, _ := sqltype.Integer(types[sqltype.Int4], 2)
		got, err := bound.support.InRange(ctx, val, base, offset, true, false, sqltype.InputEnvironment{})
		if err != nil || !got {
			t.Fatal(got, err)
		}
		if _, err := old.support.InRange(ctx, val, base, offset, true, false, sqltype.InputEnvironment{}); err == nil {
			t.Fatal("original support accepted replacement authority")
		}
	}
	if original.specs[0].frame != before {
		t.Fatal("remap mutated original frame")
	}
}

func TestQueryWindowRangeNamedFrameCloneIdentity(t *testing.T) {
	_, query, _ := queryCoreFixture(t, "SELECT count(*) OVER w,count(*) OVER w,count(*) OVER v FROM t WINDOW w AS(ORDER BY id RANGE 1 PRECEDING),v AS(ORDER BY id RANGE 1 PRECEDING)")
	project := query.Root.(*projectNode)
	original := project.child.(*windowNode)
	if len(original.specs) != 2 || len(original.calls) != 2 || original.specs[0].definition != 1 || original.specs[1].definition != 2 || !reflect.DeepEqual(original.sortIndices, []int{0, 0}) || !reflect.DeepEqual(original.specIndices, []int{0, 1}) {
		t.Fatal("named frame identity or shared sort", original)
	}
	a, b, c := project.expressions[0].(*windowExpression), project.expressions[1].(*windowExpression), project.expressions[2].(*windowExpression)
	if a.slot != b.slot || a.slot == c.slot {
		t.Fatal("same-name/distinct-name call slots", a, b, c)
	}
	copied, err := rewriteQueryExpressions(query.Root, func(expr expr.Expr) (expr.Expr, bool, error) {
		literal, ok := expr.(literalExpression)
		if !ok {
			return expr, false, nil
		}
		value, err := sqltype.Integer(literal.ResultUsage().Type, 2)
		literal.value = value
		return literal, true, err
	})
	if err != nil || copied == query.Root {
		t.Fatal(copied, err)
	}
	next := copied.(*projectNode).child.(*windowNode)
	if next == original || !reflect.DeepEqual(next.sortIndices, original.sortIndices) {
		t.Fatal("clone/sort ownership", next)
	}
	evaluation := EvalContext{evalStatement: evalStatement{Context: context.Background()}}
	for i, spec := range original.specs {
		if next.specs[i].definition != spec.definition {
			t.Fatal("rewrite lost named identity")
		}
		oldValue, err := spec.frame.start.offset.Eval(&evaluation)
		if err != nil {
			t.Fatal(err)
		}
		newValue, err := next.specs[i].frame.start.offset.Eval(&evaluation)
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
