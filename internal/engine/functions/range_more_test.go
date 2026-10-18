package functions

import (
	"context"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func TestRangeMoreScalarCarrier(t *testing.T) {
	old, fresh := inspectCarrierTypes(), inspectCarrierTypes()
	for _, id := range []sqltype.ID{sqltype.Int4Range, sqltype.Int8Range, sqltype.NumRange} {
		r, nextRange := old[id], fresh[id]
		m, nextMulti := sqltype.RangeInspectResult(sqltype.RangeConstruct, r), sqltype.RangeInspectResult(sqltype.RangeConstruct, nextRange)
		selected, resolution := ResolveRangeInspect(sqltype.RangeConstruct, true, []Argument{{Usage: r.Usage()}})
		if selected != r || resolution != Resolved {
			t.Fatal(selected, resolution)
		}
		var order []int
		argument := stringUnitArgument{Info: expr.Info{Usage: r.Usage()}, value: sqltype.Null(r), order: &order}
		node, err := NewRangeInspect(expr.Info{Usage: m.Usage()}, sqltype.RangeConstruct, []expr.Expr{argument})
		if err != nil || node.Validate() != nil {
			t.Fatal(err)
		}
		value, err := node.Eval(&stringUnitContext{Context: context.Background()})
		if err != nil || !value.IsNull() || value.Type() != m || len(order) != 1 {
			t.Fatal(value, err, order)
		}
		argument.Info.Usage, argument.value = nextRange.Usage(), sqltype.Null(nextRange)
		next, err := node.Reconstruct(expr.Info{Usage: nextMulti.Usage()}, []expr.Expr{argument})
		if err != nil || next.Validate() != nil || next.Plan == node.Plan {
			t.Fatal("reconstruction", err)
		}
		if _, err := node.Reconstruct(node.Info, []expr.Expr{argument}); err == nil {
			t.Fatal("foreign authority")
		}
	}
	for _, tc := range []struct {
		args    []Argument
		visible bool
		want    Resolution
	}{
		{nil, true, Missing},
		{[]Argument{{}}, true, UnknownPolymorphic},
		{[]Argument{{Usage: old[sqltype.Int4Multirange].Usage()}}, true, Missing},
		{[]Argument{{Usage: old[sqltype.Int4Range].Usage()}}, false, Missing},
		{[]Argument{{Usage: old[sqltype.Int4Range].Usage()}, {Usage: old[sqltype.Int4Range].Usage()}}, true, Missing},
	} {
		if _, got := ResolveRangeInspect(sqltype.RangeConstruct, tc.visible, tc.args); got != tc.want {
			t.Fatal(got, tc.want)
		}
	}
}
