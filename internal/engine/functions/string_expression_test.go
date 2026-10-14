package functions

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/sqltype"
	"testing"
)

type stringUnitExpression interface {
	ResultUsage() sqltype.Usage
	Eval(*stringUnitContext) (sqltype.Value, error)
}
type stringUnitContext struct{ context.Context }

func (c *stringUnitContext) FunctionContext() context.Context { return c.Context }
func (c *stringUnitContext) FunctionInput() sqltype.InputEnvironment {
	return sqltype.InputEnvironment{}
}
func (c *stringUnitContext) FunctionStringOutput() sqltype.OutputEnvironment {
	return sqltype.OutputEnvironment{}
}
func (c *stringUnitContext) FunctionLiteralNull(arg stringUnitExpression) bool {
	node, ok := arg.(stringUnitArgument)
	return ok && node.literal
}

type stringUnitArgument struct {
	Info[struct{}]
	value   sqltype.Value
	order   *[]int
	index   int
	err     error
	literal bool
}

func (a stringUnitArgument) Eval(*stringUnitContext) (sqltype.Value, error) {
	*a.order = append(*a.order, a.index)
	return a.value, a.err
}
func TestStringRuntimeDemand(t *testing.T) {
	var text *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Text {
			text = d
		}
	}
	plan, err := sqltype.BindString("||", text.Usage(), text.Usage(), []sqltype.Usage{text.Usage(), text.Usage()}, [4]bool{true, true}, false)
	if err != nil {
		t.Fatal(err)
	}
	var order []int
	node := String[stringUnitExpression, *stringUnitContext, struct{}]{Info: Info[struct{}]{Usage: text.Usage()}, Plan: plan, Textual: [4]bool{true, true}, Args: []stringUnitExpression{
		stringUnitArgument{Info: Info[struct{}]{Usage: text.Usage()}, value: sqltype.Null(text), order: &order, index: 0},
		stringUnitArgument{Info: Info[struct{}]{Usage: text.Usage()}, value: sqltype.String(text, "x"), order: &order, index: 1},
	}}
	got, err := node.Eval(&stringUnitContext{Context: context.Background()})
	if err != nil || !got.IsNull() || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal("children exactly once", got, order, err)
	}
	failure := errors.New("demanded sibling")
	node.Args[1] = stringUnitArgument{Info: Info[struct{}]{Usage: text.Usage()}, order: &order, index: 1, err: failure}
	order = nil
	_, err = node.Eval(&stringUnitContext{Context: context.Background()})
	if !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal("strict NULL suppressed sibling", order, err)
	}
}
