package functions

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPosixEvaluationDemand(t *testing.T) {
	types := formatUnitTypes()
	text, boolean := types[sqltype.Text], types[sqltype.Bool]
	provider, err := sqltype.BindPosix("~", text.Usage(), text.Usage(), boolean.Usage(), text.Usage().Collation, false)
	if err != nil {
		t.Fatal(err)
	}
	extraction, err := sqltype.BindPosixSubstring([2]sqltype.Usage{text.Usage(), text.Usage()}, text.Usage(), text.Usage().Collation, false)
	if err != nil {
		t.Fatal(err)
	}
	var order []int
	failure := errors.New("demanded row pattern")
	left := regexpUnitArgument{Info: Info[struct{}]{Usage: text.Usage()}, value: sqltype.Null(text), order: &order, index: 0}
	right := regexpUnitArgument{Info: Info[struct{}]{Usage: text.Usage()}, err: failure, order: &order, index: 1}
	predicate := Posix[regexpUnitExpression, *regexpUnitContext, struct{}]{Info: Info[struct{}]{Usage: boolean.Usage()}, Left: left, Right: right, Provider: provider, Operator: "~"}
	substring := PosixSubstring[regexpUnitExpression, *regexpUnitContext, struct{}]{Info: Info[struct{}]{Usage: text.Usage()}, Args: [2]regexpUnitExpression{left, right}, Provider: extraction}
	for _, node := range []regexpUnitExpression{predicate, substring} {
		order = nil
		value, err := node.Eval(&regexpUnitContext{Context: context.Background()})
		if err != nil || !value.IsNull() || !reflect.DeepEqual(order, []int{0}) {
			t.Fatal(value, err, order)
		}
	}
	left.value = sqltype.String(text, "a")
	predicate.Left = left
	substring.Args[0] = left
	for _, node := range []regexpUnitExpression{predicate, substring} {
		order = nil
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
}
