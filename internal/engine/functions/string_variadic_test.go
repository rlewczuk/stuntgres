package functions

import (
	"context"
	"errors"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func formatUnitTypes() map[sqltype.ID]*sqltype.Descriptor {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, descriptor := range sqltype.Builtins() {
		types[descriptor.ID()] = descriptor
	}
	return types
}
func TestStringVariadicArrayBorrowing(t *testing.T) {
	types := formatUnitTypes()
	arrayType := types[types[sqltype.Int4].AssociatedArray()]
	one, err := sqltype.Integer(types[sqltype.Int4], 1)
	if err != nil {
		t.Fatal(err)
	}
	two, err := sqltype.Integer(types[sqltype.Int4], 2)
	if err != nil {
		t.Fatal(err)
	}
	value, err := sqltype.ArrayValue(arrayType, []sqltype.ArrayDimension{{Lower: -2, Length: 2}, {Lower: 3, Length: 2}}, []sqltype.Value{one, sqltype.Null(types[sqltype.Int4]), two, one})
	if err != nil {
		t.Fatal(err)
	}
	borrowed := FormatValues{Array: value}
	if borrowed.Len() != 4 || !sqltype.SameDatum(borrowed.At(0), one) || !borrowed.At(1).IsNull() || !sqltype.SameDatum(borrowed.At(2), two) {
		t.Fatal("lost existing row-major payload")
	}
	if n := testing.AllocsPerRun(100, func() {
		for i := range borrowed.Len() {
			if borrowed.At(i).Type() != types[sqltype.Int4] {
				panic("borrowed element changed")
			}
		}
	}); n != 0 {
		t.Fatal("array borrowing allocates", n)
	}
	domain, err := sqltype.Domain(90002, "sv_ints", "sv_ints", arrayType.Usage())
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := domain.Wrap(value)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []sqltype.Value{value, wrapped} {
		var order []int
		arg := stringUnitArgument{Info: expr.Info{Usage: input.Type().Usage()}, value: input, order: &order}
		node := StringVariadic{Info: expr.Info{Usage: types[sqltype.Text].Usage()}, Name: "concat", Args: []expr.Expr{arg}, Variadic: true, Stable: true}
		got, err := node.Eval(&stringUnitContext{Context: context.Background()})
		if err != nil {
			t.Fatal(err)
		}
		text, err := got.Text()
		if err != nil || text != "121" {
			t.Fatal(text, err)
		}
	}
	if value.ArrayDimensions()[0].Lower != -2 || value.ArrayDimensions()[1].Lower != 3 || !sqltype.SameDatum(value.ArrayElement(2), two) {
		t.Fatal("source array mutated")
	}
}
func TestStringVariadicOutputSize(t *testing.T) {
	_, err := formatSize(int(^uint(0)>>1), 1)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "54000" || diagnostic.Message != "requested length too large" {
		t.Fatal("unchecked output size", err)
	}
}
