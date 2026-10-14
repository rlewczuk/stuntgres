package functions

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestStringReplacementFourChildren(t *testing.T) {
	var text, integer *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		switch d.ID() {
		case sqltype.Text:
			text = d
		case sqltype.Int4:
			integer = d
		}
	}
	two, _ := sqltype.Integer(integer, 2)
	one, _ := sqltype.Integer(integer, 1)
	plan, err := sqltype.BindString("overlay", text.Usage(), text.Usage(), []sqltype.Usage{text.Usage(), text.Usage(), integer.Usage(), integer.Usage()}, [4]bool{true, true}, false)
	if err != nil {
		t.Fatal(err)
	}
	var order []int
	node := String[stringUnitExpression, *stringUnitContext, struct{}]{Info: Info[struct{}]{Usage: text.Usage()}, Plan: plan, Textual: [4]bool{true, true}, Syntax: parser.OverlayStringSyntax, Args: []stringUnitExpression{
		stringUnitArgument{Info: Info[struct{}]{Usage: text.Usage()}, value: sqltype.Null(text), order: &order, index: 0},
		stringUnitArgument{Info: Info[struct{}]{Usage: text.Usage()}, value: sqltype.String(text, "X"), order: &order, index: 1},
		stringUnitArgument{Info: Info[struct{}]{Usage: integer.Usage()}, value: two, order: &order, index: 2},
		stringUnitArgument{Info: Info[struct{}]{Usage: integer.Usage()}, value: one, order: &order, index: 3},
	}}
	got, err := node.Eval(&stringUnitContext{Context: context.Background()})
	if err != nil || !got.IsNull() || !reflect.DeepEqual(order, []int{0, 1, 2, 3}) {
		t.Fatal(got, order, err)
	}
	failure := errors.New("fourth child demanded")
	node.Args = append([]stringUnitExpression(nil), node.Args...)
	node.Args[3] = stringUnitArgument{Info: Info[struct{}]{Usage: integer.Usage()}, order: &order, index: 3, err: failure}
	order = nil
	if _, err := node.Eval(&stringUnitContext{Context: context.Background()}); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0, 1, 2, 3}) {
		t.Fatal(order, err)
	}
}
