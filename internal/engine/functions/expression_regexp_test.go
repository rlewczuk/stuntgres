package functions

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"testing"
)

type regexpUnitContext struct {
	unitCapabilities
	context.Context
	execution sqltype.PGRegexExecution
}

func (c *regexpUnitContext) FunctionContext() context.Context                  { return c.Context }
func (c *regexpUnitContext) FunctionRegexExecution() *sqltype.PGRegexExecution { return &c.execution }

type regexpUnitArgument struct {
	expr.Info
	value sqltype.Value
	order *[]int
	index int
	err   error
}

func (arg regexpUnitArgument) Eval(expr.Context) (sqltype.Value, error) {
	*arg.order = append(*arg.order, arg.index)
	return arg.value, arg.err
}
func TestRegexpScalarStrictChildren(t *testing.T) {
	types := formatUnitTypes()
	text, boolean := types[sqltype.Text], types[sqltype.Bool]
	plan, err := sqltype.BindRegexpFunction("regexp_like", []sqltype.Usage{text.Usage(), text.Usage()}, boolean.Usage(), text.Usage().Collation, false)
	if err != nil {
		t.Fatal(err)
	}
	var order []int
	failure := errors.New("later strict sibling")
	null := regexpUnitArgument{Info: expr.Info{Usage: text.Usage()}, value: sqltype.Null(text), order: &order, index: 0}
	pattern := regexpUnitArgument{Info: expr.Info{Usage: text.Usage()}, err: failure, order: &order, index: 1}
	node := Regexp{RegexpCall{Info: expr.Info{Usage: boolean.Usage()}, Plan: plan, Args: []expr.Expr{null, pattern}, WrittenSlots: [7]uint8{0, 1}}}
	if _, err := node.Eval(&regexpUnitContext{Context: context.Background()}); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal(err, order)
	}
	pattern.err = nil
	pattern.value = sqltype.String(text, "[")
	node.Args[1] = pattern
	order = nil
	value, err := node.Eval(&regexpUnitContext{Context: context.Background()})
	if err != nil || !value.IsNull() || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal(value, err, order)
	}
	node.Args = []expr.Expr{pattern, null}
	node.WrittenSlots = [7]uint8{1, 0}
	order = nil
	value, err = node.Eval(&regexpUnitContext{Context: context.Background()})
	if err != nil || !value.IsNull() || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal(value, err, order)
	}
}
