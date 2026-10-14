package functions

import (
	"context"
	"errors"
	"reflect"
	"testing"

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
	expr := Math[functionTestExpression, *functionTestContext, struct{}]{Info: Info[struct{}]{Usage: numeric.Usage()}, Plan: plan, Args: []functionTestExpression{
		functionTestArgument{Info: Info[struct{}]{Usage: numeric.Usage()}, value: sqltype.Null(numeric), order: &order, index: 0},
		functionTestArgument{Info: Info[struct{}]{Usage: int4.Usage()}, value: one, order: &order, index: 1},
	}}
	c := &functionTestContext{context.Background()}
	got, err := expr.Eval(c)
	if err != nil || !got.IsNull() || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal(got, order, err)
	}
	failure := errors.New("demanded sibling")
	expr.Args[1] = functionTestArgument{Info: Info[struct{}]{Usage: int4.Usage()}, order: &order, index: 1, err: failure}
	order = nil
	_, err = expr.Eval(c)
	if !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal(order, err)
	}
}
