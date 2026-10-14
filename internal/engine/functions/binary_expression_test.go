package functions

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/sqltype"
	"testing"
)

type binaryUnitExpression interface {
	ResultUsage() sqltype.Usage
	Eval(*binaryUnitContext) (sqltype.Value, error)
}
type binaryUnitContext struct{ context.Context }

func (c *binaryUnitContext) FunctionContext() context.Context { return c.Context }
func (c *binaryUnitContext) FunctionLiteralNull(arg binaryUnitExpression) bool {
	value, ok := arg.(binaryUnitArgument)
	return ok && value.literal
}
func (c *binaryUnitContext) FunctionBinaryConversion(int, int) (*sqltype.EncodingConversionHandler, error) {
	panic("immutable binary unit requested conversion authority")
}

type binaryUnitArgument struct {
	Info[struct{}]
	value   sqltype.Value
	order   *[]int
	index   int
	err     error
	literal bool
}

func (a binaryUnitArgument) Eval(*binaryUnitContext) (sqltype.Value, error) {
	if a.order != nil {
		*a.order = append(*a.order, a.index)
	}
	return a.value, a.err
}
func TestBinaryBasicEvaluationTraversal(t *testing.T) {
	var d *sqltype.Descriptor
	for _, v := range sqltype.Builtins() {
		if v.ID() == sqltype.Bytea {
			d = v
		}
	}
	plan, err := sqltype.BindBinary("btrim", d.Usage(), d.Usage(), d.Usage())
	if err != nil {
		t.Fatal(err)
	}
	var order []int
	set, _ := sqltype.Binary(d, "\x00")
	expr := Binary[binaryUnitExpression, *binaryUnitContext, struct{}]{Info: Info[struct{}]{Usage: d.Usage()}, Plan: plan, Args: []binaryUnitExpression{
		binaryUnitArgument{Info: Info[struct{}]{Usage: d.Usage()}, value: sqltype.Null(d), order: &order, index: 0},
		binaryUnitArgument{Info: Info[struct{}]{Usage: d.Usage()}, value: set, order: &order, index: 1},
	}}
	value, err := expr.Eval(&binaryUnitContext{Context: context.Background()})
	if err != nil || !value.IsNull() || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal(value, order, err)
	}
	failure := errors.New("demanded binary sibling")
	expr.Args[1] = binaryUnitArgument{Info: Info[struct{}]{Usage: d.Usage()}, order: &order, index: 1, err: failure}
	order = nil
	if _, err := expr.Eval(&binaryUnitContext{Context: context.Background()}); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal(order, err)
	}
}
