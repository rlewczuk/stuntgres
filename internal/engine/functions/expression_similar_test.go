package functions

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestSimilarEvaluationDemand(t *testing.T) {
	types := formatUnitTypes()
	text, boolean := types[sqltype.Text], types[sqltype.Bool]
	provider, err := sqltype.BindSimilar("~", text.Usage(), text.Usage(), boolean.Usage(), text.Usage().Collation, false)
	if err != nil {
		t.Fatal(err)
	}
	var order []int
	failure := errors.New("demanded row pattern")
	left := regexpUnitArgument{Info: Info[struct{}]{Usage: text.Usage()}, value: sqltype.Null(text), order: &order, index: 0}
	right := regexpUnitArgument{Info: Info[struct{}]{Usage: text.Usage()}, err: failure, order: &order, index: 1}
	node := Similar[regexpUnitExpression, *regexpUnitContext, struct{}]{Info: Info[struct{}]{Usage: boolean.Usage()}, Left: left, Right: right, Provider: provider, Operator: "~"}
	value, err := node.Eval(&regexpUnitContext{Context: context.Background()})
	if err != nil || !value.IsNull() || !reflect.DeepEqual(order, []int{0}) {
		t.Fatal(value, err, order)
	}
	order = nil
	left.value = sqltype.String(text, "a")
	node.Left = left
	if _, err := node.Eval(&regexpUnitContext{Context: context.Background()}); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal(err, order)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	order = nil
	if _, err := node.Eval(&regexpUnitContext{Context: canceled}); !errors.Is(err, context.Canceled) || len(order) != 0 {
		t.Fatal(err, order)
	}
}
