package functions

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestJSONStripNullsExpressionDemand(t *testing.T) {
	testStripExpressionDemand(t, sqltype.JSON)
}

func TestJSONBStripNullsExpressionDemand(t *testing.T) {
	testStripExpressionDemand(t, sqltype.JSONB)
}

func testStripExpressionDemand(t *testing.T, id sqltype.ID) {
	d := temporalTestTypes()
	plan, err := sqltype.BindJSONStripNulls(d[id].Usage(), d[id].Usage(), d[sqltype.Bool].Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, written := range []int{1, 2} {
		base := JSONStripNulls{Info: expr.Info{Usage: d[id].Usage()}, Plan: plan, WrittenCount: written, WrittenSlots: [2]uint8{0, 1}}
		if written == 2 {
			base.WrittenSlots = [2]uint8{1, 0}
		}
		for failing := -1; failing < 2; failing++ {
			e := base
			var order []int
			failure := errors.New("ordinary child failure after strict NULL")
			for i, id := range []sqltype.ID{id, sqltype.Bool} {
				a := temporalTestArgument{Info: expr.Info{Usage: d[id].Usage()}, value: sqltype.Null(d[id]), order: &order, index: i}
				if failing == i {
					a.err = failure
				}
				e.Args[i] = a
			}
			got, err := e.Eval(&temporalTestContext{Context: context.Background()})
			want := []int{0, 1}
			if written == 2 {
				want = []int{1, 0}
			}
			if failing >= 0 {
				if want[0] == failing {
					want = want[:1]
				}
				if !errors.Is(err, failure) || got != (sqltype.Value{}) {
					t.Fatal(got, err)
				}
			} else if err != nil || !got.IsNull() || got.Type() != d[id] {
				t.Fatal(got, err)
			}
			if !reflect.DeepEqual(order, want) {
				t.Fatal("written/default evaluation order", order, want)
			}
		}
	}
}

func TestJSONStripNullsExpressionReconstruct(t *testing.T) {
	testStripExpressionReconstruct(t, sqltype.JSON)
}

func TestJSONBStripNullsExpressionReconstruct(t *testing.T) {
	testStripExpressionReconstruct(t, sqltype.JSONB)
}

func testStripExpressionReconstruct(t *testing.T, id sqltype.ID) {
	d, fresh := temporalTestTypes(), temporalTestTypes()
	e := JSONStripNulls{Info: expr.Info{Usage: d[id].Usage()}, WrittenCount: 2, WrittenSlots: [2]uint8{1, 0}, Labels: [2]string{"strip_in_arrays", "target"}}
	var args [2]expr.Expr
	for i, id := range []sqltype.ID{id, sqltype.Bool} {
		e.Args[i] = temporalTestLiteral(sqltype.Null(d[id]))
		args[i] = temporalTestLiteral(sqltype.Null(fresh[id]))
	}
	plan, err := sqltype.BindJSONStripNulls(e.Usage, e.Args[0].ResultUsage(), e.Args[1].ResultUsage())
	if err != nil {
		t.Fatal(err)
	}
	e.Plan = plan
	next, err := e.Reconstruct(expr.Info{Usage: fresh[id].Usage()}, args)
	if err != nil || next.Usage.Type != fresh[id] || next.WrittenSlots != e.WrittenSlots || next.Labels != e.Labels || next.WrittenCount != 2 {
		t.Fatal(next, err)
	}
	got, err := next.Eval(&temporalTestContext{Context: context.Background()})
	if err != nil || got.Type() != fresh[id] || !got.IsNull() {
		t.Fatal(got, err)
	}
	args[0] = e.Args[0]
	if _, err := e.Reconstruct(expr.Info{Usage: fresh[id].Usage()}, args); err == nil {
		t.Fatal("accepted foreign target authority")
	}
}
