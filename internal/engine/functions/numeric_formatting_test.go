package functions

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"testing"
)

type numericFormattingUnitContext struct {
	unitCapabilities
	context.Context
}

func (c *numericFormattingUnitContext) FunctionContext() context.Context { return c.Context }
func (c *numericFormattingUnitContext) FunctionInput() sqltype.InputEnvironment {
	return sqltype.InputEnvironment{}
}
func (c *numericFormattingUnitContext) FunctionOutput() sqltype.OutputEnvironment {
	return sqltype.OutputEnvironment{}
}

type numericFormattingUnitArgument struct {
	expr.Info
	value sqltype.Value
	order *[]int
	index int
	err   error
}

func (a numericFormattingUnitArgument) Eval(expr.Context) (sqltype.Value, error) {
	if a.order != nil {
		*a.order = append(*a.order, a.index)
	}
	return a.value, a.err
}
func TestNumericFormattingRuntimeDemand(t *testing.T) {
	types := formatUnitTypes()
	numeric, text := types[sqltype.Numeric], types[sqltype.Text]
	mask := "9.9.9"
	plan, err := sqltype.BindNumericFormat(context.Background(), "to_char", text.Usage(), numeric.Usage(), &mask)
	if err != nil {
		t.Fatal(err)
	}
	var order []int
	node := NumericFormatting{Info: expr.Info{Usage: text.Usage()}, Plan: plan, Args: [2]expr.Expr{
		numericFormattingUnitArgument{Info: expr.Info{Usage: numeric.Usage()}, value: sqltype.Null(numeric), order: &order, index: 0},
		numericFormattingUnitArgument{Info: expr.Info{Usage: text.Usage()}, value: sqltype.String(text, mask), order: &order, index: 1},
	}}
	value, err := node.Eval(&numericFormattingUnitContext{Context: context.Background()})
	if err != nil || !value.IsNull() || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal(value, order, err)
	}
	failure := errors.New("demanded template sibling")
	node.Args[0] = numericFormattingUnitArgument{Info: expr.Info{Usage: numeric.Usage()}, value: sqltype.Null(numeric)}
	node.Args[1] = numericFormattingUnitArgument{Info: expr.Info{Usage: text.Usage()}, order: &order, index: 1, err: failure}
	order = nil
	if _, err := node.Eval(&numericFormattingUnitContext{Context: context.Background()}); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{1}) {
		t.Fatal("literal NULL bypassed sibling", order, err)
	}
}
