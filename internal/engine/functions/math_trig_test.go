package functions

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/sqltype"
)

type functionTestExpression interface {
	ResultUsage() sqltype.Usage
	Eval(*functionTestContext) (sqltype.Value, error)
}
type functionTestContext struct{ context.Context }

func (c *functionTestContext) FunctionContext() context.Context { return c.Context }
func (c *functionTestContext) FunctionInput() sqltype.InputEnvironment {
	return sqltype.InputEnvironment{}
}
func (c *functionTestContext) FunctionLiteralNull(e functionTestExpression) bool {
	argument, ok := e.(functionTestArgument)
	return ok && argument.literal && argument.value.IsNull()
}

type functionTestArgument struct {
	Info[struct{}]
	value   sqltype.Value
	order   *[]int
	index   int
	err     error
	literal bool
}

func (e functionTestArgument) Eval(*functionTestContext) (sqltype.Value, error) {
	if e.order != nil {
		*e.order = append(*e.order, e.index)
	}
	return e.value, e.err
}
func TestMathTrigRuntimeDemand(t *testing.T) {
	var d *sqltype.Descriptor
	for _, candidate := range sqltype.Builtins() {
		if candidate.ID() == sqltype.Float8 {
			d = candidate
		}
	}
	plan, err := sqltype.BindMath("atan2d", d.Usage(), d.Usage(), d.Usage())
	if err != nil {
		t.Fatal(err)
	}
	one, _ := sqltype.Float(d, 1)
	var order []int
	expr := Math[functionTestExpression, *functionTestContext, struct{}]{Info: Info[struct{}]{Usage: d.Usage()}, Plan: plan, Args: []functionTestExpression{
		functionTestArgument{Info: Info[struct{}]{Usage: d.Usage()}, value: sqltype.Null(d), order: &order, index: 0},
		functionTestArgument{Info: Info[struct{}]{Usage: d.Usage()}, value: one, order: &order, index: 1},
	}}
	c := &functionTestContext{context.Background()}
	got, err := expr.Eval(c)
	if err != nil || got != sqltype.Null(d) || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal("runtime NULL demands siblings", got, order, err)
	}
	demanded := errors.New("demanded inverse sibling")
	expr.Args[1] = functionTestArgument{Info: Info[struct{}]{Usage: d.Usage()}, order: &order, index: 1, err: demanded}
	order = nil
	_, err = expr.Eval(c)
	if !errors.Is(err, demanded) || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal("runtime demand failure", order, err)
	}
	expr.Args[0] = functionTestArgument{Info: Info[struct{}]{Usage: d.Usage()}, value: sqltype.Null(d), literal: true}
	order = nil
	got, err = expr.Eval(c)
	if err != nil || got != sqltype.Null(d) || len(order) != 0 {
		t.Fatal("literal NULL eliminated runtime sibling", got, order, err)
	}
	for _, name := range []string{"cos", "cosd", "cot", "cotd", "sin", "sind", "tan", "tand"} {
		plan, err := sqltype.BindMath(name, d.Usage(), d.Usage())
		if err != nil {
			t.Fatal(err)
		}
		order = nil
		expr := Math[functionTestExpression, *functionTestContext, struct{}]{Info: Info[struct{}]{Usage: d.Usage()}, Plan: plan, Args: []functionTestExpression{
			functionTestArgument{Info: Info[struct{}]{Usage: d.Usage()}, value: sqltype.Null(d), order: &order, index: 0},
		}}
		got, err := expr.Eval(c)
		if err != nil || got != sqltype.Null(d) || !reflect.DeepEqual(order, []int{0}) {
			t.Fatal("direct runtime typed NULL", name, got, order, err)
		}
		order = nil
		expr.Args[0] = functionTestArgument{Info: Info[struct{}]{Usage: d.Usage()}, order: &order, index: 0, err: demanded}
		_, err = expr.Eval(c)
		if !errors.Is(err, demanded) || !reflect.DeepEqual(order, []int{0}) {
			t.Fatal("direct runtime argument failure", name, order, err)
		}
	}
}

func TestMathTrigSignatures(t *testing.T) {
	for _, name := range []string{"acos", "acosd", "asin", "asind", "atan", "atand", "atan2", "atan2d", "cos", "cosd", "cot", "cotd", "sin", "sind", "tan", "tand"} {
		binary := name == "atan2" || name == "atan2d"
		arity := 1
		if binary {
			arity = 2
		}
		signatures := mathSignatures(name)
		if len(signatures) != 1 || signatures[0].Arity != arity || signatures[0].Result != sqltype.Float8 || signatures[0].Args[0] != sqltype.Float8 || binary && signatures[0].Args[1] != sqltype.Float8 {
			t.Fatal("exact FLOAT8 signature", name, signatures)
		}
	}
}
