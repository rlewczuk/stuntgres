package functions

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestStringLengthRuntimeDemand(t *testing.T) {
	var text, integer *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		switch d.ID() {
		case sqltype.Text:
			text = d
		case sqltype.Int4:
			integer = d
		}
	}
	plan, err := sqltype.BindString("strpos", integer.Usage(), text.Usage(), []sqltype.Usage{text.Usage(), text.Usage()}, [4]bool{true, true}, false)
	if err != nil {
		t.Fatal(err)
	}
	var order []int
	node := String[stringUnitExpression, *stringUnitContext, struct{}]{Info: Info[struct{}]{Usage: integer.Usage()}, Plan: plan, Textual: [4]bool{true, true}, Args: []stringUnitExpression{
		stringUnitArgument{Info: Info[struct{}]{Usage: text.Usage()}, value: sqltype.Null(text), order: &order, index: 0},
		stringUnitArgument{Info: Info[struct{}]{Usage: text.Usage()}, value: sqltype.String(text, "x"), order: &order, index: 1},
	}}
	got, err := node.Eval(&stringUnitContext{Context: context.Background()})
	if err != nil || !got.IsNull() || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal("child demand", got, order, err)
	}
	failure := errors.New("demanded search sibling")
	node.Args[1] = stringUnitArgument{Info: Info[struct{}]{Usage: text.Usage()}, order: &order, index: 1, err: failure}
	order = nil
	if _, err := node.Eval(&stringUnitContext{Context: context.Background()}); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal(order, err)
	}
}
