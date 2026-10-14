package functions

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestJSONBObjectExpressionEvaluationOrder(t *testing.T) {
	types := formatUnitTypes()
	array, result := types[1009], types[sqltype.JSONB]
	plan, err := sqltype.BindBinary("jsonb_object", result.Usage(), array.Usage(), array.Usage())
	if err != nil {
		t.Fatal(err)
	}
	var order []int
	value, err := array.Input(context.Background(), "{x}", -1)
	if err != nil {
		t.Fatal(err)
	}
	expr := Binary[binaryUnitExpression, *binaryUnitContext, struct{}]{Info: Info[struct{}]{Usage: result.Usage()}, Plan: plan, Args: []binaryUnitExpression{
		binaryUnitArgument{Info: Info[struct{}]{Usage: array.Usage()}, value: sqltype.Null(array), order: &order, index: 0},
		binaryUnitArgument{Info: Info[struct{}]{Usage: array.Usage()}, value: value, order: &order, index: 1},
	}}
	got, err := expr.Eval(&binaryUnitContext{Context: context.Background()})
	if err != nil || !got.IsNull() || got.Type() != result || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal(got, order, err)
	}
	failure := errors.New("demanded JSONB object sibling")
	expr.Args[1] = binaryUnitArgument{Info: Info[struct{}]{Usage: array.Usage()}, order: &order, index: 1, err: failure}
	order = nil
	if _, err := expr.Eval(&binaryUnitContext{Context: context.Background()}); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal(order, err)
	}
	expr.Args[0] = binaryUnitArgument{Info: Info[struct{}]{Usage: array.Usage()}, order: &order, index: 0, err: failure}
	order = nil
	if _, err := expr.Eval(&binaryUnitContext{Context: context.Background()}); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0}) {
		t.Fatal(order, err)
	}
}
