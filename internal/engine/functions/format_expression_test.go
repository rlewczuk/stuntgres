package functions

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"testing"
)

type formatUnitContext struct {
	unitCapabilities
	context.Context
}

func (c *formatUnitContext) FunctionContext() context.Context { return c.Context }
func (c *formatUnitContext) FunctionFormatOutput() sqltype.OutputEnvironment {
	return sqltype.OutputEnvironment{}
}

type formatUnitArgument struct {
	expr.Info
	value sqltype.Value
	order *[]int
	index int
	err   error
}

func (a formatUnitArgument) Eval(expr.Context) (sqltype.Value, error) {
	*a.order = append(*a.order, a.index)
	return a.value, a.err
}
func TestFormatRuntimeDemand(t *testing.T) {
	types := formatUnitTypes()
	text, integer := types[sqltype.Text], types[sqltype.Int4]
	for _, nullPattern := range []bool{false, true} {
		pattern := sqltype.String(text, "constant")
		if nullPattern {
			pattern = sqltype.Null(text)
		}
		var order []int
		node := Format{Info: expr.Info{Usage: text.Usage()}, Int4: integer, Args: []expr.Expr{
			formatUnitArgument{Info: expr.Info{Usage: text.Usage()}, value: pattern, order: &order, index: 0},
			formatUnitArgument{Info: expr.Info{Usage: integer.Usage()}, value: sqltype.Null(integer), order: &order, index: 1},
		}}
		value, err := node.Eval(&formatUnitContext{Context: context.Background()})
		if err != nil || value.Type() != text || value.IsNull() != nullPattern || !reflect.DeepEqual(order, []int{0, 1}) {
			t.Fatal("nonstrict children exactly once", value, order, err)
		}
		if !nullPattern {
			actual, err := value.Text()
			if err != nil || actual != "constant" {
				t.Fatal(actual, err)
			}
		}
		failure := errors.New("reached format child")
		node.Args[1] = formatUnitArgument{Info: expr.Info{Usage: integer.Usage()}, order: &order, index: 1, err: failure}
		order = nil
		value, err = node.Eval(&formatUnitContext{Context: context.Background()})
		if err != failure || value.Type() != nil || !reflect.DeepEqual(order, []int{0, 1}) {
			t.Fatal("NULL or unused format hid child failure", value, order, err)
		}
	}
}
