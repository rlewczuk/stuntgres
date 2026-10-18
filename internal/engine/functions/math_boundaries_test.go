package functions

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func TestMathTraversalRuntime(t *testing.T) {
	var numeric, int4 *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Numeric {
			numeric = d
		}
		if d.ID() == sqltype.Int4 {
			int4 = d
		}
	}
	plan, err := sqltype.BindMath("round", numeric.Usage(), numeric.Usage(), int4.Usage())
	if err != nil {
		t.Fatal(err)
	}
	var order []int
	one, _ := sqltype.Integer(int4, 1)
	node := Math{Info: expr.Info{Usage: numeric.Usage()}, Plan: plan, Args: []expr.Expr{
		functionTestArgument{Info: expr.Info{Usage: numeric.Usage()}, value: sqltype.Null(numeric), order: &order, index: 0},
		functionTestArgument{Info: expr.Info{Usage: int4.Usage()}, value: one, order: &order, index: 1},
	}}
	c := &functionTestContext{Context: context.Background()}
	got, err := node.Eval(c)
	if err != nil || !got.IsNull() || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal(got, order, err)
	}
	failure := errors.New("demanded sibling")
	node.Args[1] = functionTestArgument{Info: expr.Info{Usage: int4.Usage()}, order: &order, index: 1, err: failure}
	order = nil
	_, err = node.Eval(c)
	if !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal(order, err)
	}
}
