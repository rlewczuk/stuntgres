package functions

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/expr"
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
	node := Binary{Info: expr.Info{Usage: bytea.Usage()}, Plan: plan}
	for i, value := range []sqltype.Value{a, r, s, n} {
		node.Args = append(node.Args, binaryUnitArgument{Info: expr.Info{Usage: value.Type().Usage()}, value: value, order: &order, index: i})
	}
	got, err := node.Eval(&binaryUnitContext{Context: context.Background()})
	raw, _ := got.Binary()
	if err != nil || raw != "abXYef" || !reflect.DeepEqual(order, []int{0, 1, 2, 3}) {
		t.Fatal(raw, order, err)
	}
	failure := errors.New("fourth child failure")
	node.Args[0] = binaryUnitArgument{Info: expr.Info{Usage: bytea.Usage()}, value: sqltype.Null(bytea), order: &order, index: 0}
	node.Args[3] = binaryUnitArgument{Info: expr.Info{Usage: integer.Usage()}, order: &order, index: 3, err: failure}
	order = nil
	if _, err := node.Eval(&binaryUnitContext{Context: context.Background()}); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0, 1, 2, 3}) {
		t.Fatal(order, err)
	}
}
