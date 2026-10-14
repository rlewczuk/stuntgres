package functions

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestBinarySliceHeterogeneousTraversal(t *testing.T) {
	types := formatUnitTypes()
	bytea, integer := types[sqltype.Bytea], types[sqltype.Int4]
	plan, err := sqltype.BindBinary("overlay", bytea.Usage(), bytea.Usage(), bytea.Usage(), integer.Usage(), integer.Usage())
	if err != nil {
		t.Fatal(err)
	}
	a, _ := sqltype.Binary(bytea, "abcdef")
	r, _ := sqltype.Binary(bytea, "XY")
	s, _ := sqltype.Integer(integer, 3)
	n, _ := sqltype.Integer(integer, 2)
	var order []int
	expr := Binary[binaryUnitExpression, *binaryUnitContext, struct{}]{Info: Info[struct{}]{Usage: bytea.Usage()}, Plan: plan}
	for i, value := range []sqltype.Value{a, r, s, n} {
		expr.Args = append(expr.Args, binaryUnitArgument{Info: Info[struct{}]{Usage: value.Type().Usage()}, value: value, order: &order, index: i})
	}
	got, err := expr.Eval(&binaryUnitContext{Context: context.Background()})
	raw, _ := got.Binary()
	if err != nil || raw != "abXYef" || !reflect.DeepEqual(order, []int{0, 1, 2, 3}) {
		t.Fatal(raw, order, err)
	}
	failure := errors.New("fourth child failure")
	expr.Args[0] = binaryUnitArgument{Info: Info[struct{}]{Usage: bytea.Usage()}, value: sqltype.Null(bytea), order: &order, index: 0}
	expr.Args[3] = binaryUnitArgument{Info: Info[struct{}]{Usage: integer.Usage()}, order: &order, index: 3, err: failure}
	order = nil
	if _, err := expr.Eval(&binaryUnitContext{Context: context.Background()}); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0, 1, 2, 3}) {
		t.Fatal(order, err)
	}
}
