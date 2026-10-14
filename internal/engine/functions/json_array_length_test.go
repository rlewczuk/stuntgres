package functions

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestJSONArrayLengthRuntimeDemand(t *testing.T) {
	types := formatUnitTypes()
	for _, id := range []sqltype.ID{sqltype.JSON, sqltype.JSONB} {
		name := "json_array_length"
		if id == sqltype.JSONB {
			name = "jsonb_array_length"
		}
		t.Run(name, func(t *testing.T) {
			source := types[id]
			value, err := source.Input(context.Background(), `[[],{},null]`, -1)
			if err != nil {
				t.Fatal(err)
			}
			usage := types[sqltype.Int4].Usage()
			plan, err := sqltype.BindBinary(name, usage, source.Usage())
			if err != nil {
				t.Fatal(err)
			}
			var order []int
			expr := Binary[binaryUnitExpression, *binaryUnitContext, struct{}]{Info: Info[struct{}]{Usage: usage}, Plan: plan, Args: []binaryUnitExpression{
				binaryUnitArgument{Info: Info[struct{}]{Usage: source.Usage()}, value: value, order: &order, index: 0},
			}}
			got, err := expr.Eval(&binaryUnitContext{Context: context.Background()})
			n, intErr := got.Integer()
			if err != nil || intErr != nil || n != 3 || !reflect.DeepEqual(order, []int{0}) {
				t.Fatal(got, order, err, intErr)
			}
			failure := errors.New("length child failure")
			expr.Args[0] = binaryUnitArgument{Info: Info[struct{}]{Usage: source.Usage()}, err: failure, order: &order, index: 0}
			order = nil
			if _, err := expr.Eval(&binaryUnitContext{Context: context.Background()}); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0}) {
				t.Fatal(order, err)
			}
		})
	}
}
