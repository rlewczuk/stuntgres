package functions

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

type enumUnitContext struct {
	unitCapabilities
	context.Context
}

func (c *enumUnitContext) FunctionContext() context.Context        { return c.Context }
func (c *enumUnitContext) FunctionInput() sqltype.InputEnvironment { return sqltype.InputEnvironment{} }

type enumUnitArgument struct {
	expr.Info
	value sqltype.Value
	order *[]int
	index int
	err   error
}

func (e enumUnitArgument) Eval(expr.Context) (sqltype.Value, error) {
	*e.order = append(*e.order, e.index)
	return e.value, e.err
}
func TestEnumFunctionRuntimeDemand(t *testing.T) {
	mood, err := sqltype.Enum(45000, "mood", "public.mood", []string{"z", "a", "m"})
	if err != nil {
		t.Fatal(err)
	}
	var order []int
	unary, err := NewEnum(expr.Info{Usage: mood.Usage()}, "enum_first", []expr.Expr{
		enumUnitArgument{Info: expr.Info{Usage: mood.Usage()}, value: sqltype.Null(mood), order: &order, index: 0},
	}, "mood")
	if err != nil {
		t.Fatal(err)
	}
	c := &enumUnitContext{Context: context.Background()}
	got, err := unary.Eval(c)
	if err != nil || got.IsNull() || len(order) != 1 {
		t.Fatal(got, order, err)
	}
	failure := errors.New("demanded enum argument")
	unary.Args[0] = enumUnitArgument{Info: expr.Info{Usage: mood.Usage()}, order: &order, index: 0, err: failure}
	if _, err := unary.Eval(c); !errors.Is(err, failure) {
		t.Fatal(err)
	}
}
