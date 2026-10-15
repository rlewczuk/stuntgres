package functions

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestStringSliceChildDemand(t *testing.T) {
	var text, integer *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Text {
			text = d
		}
		if d.ID() == sqltype.Int4 {
			integer = d
		}
	}
	one, _ := sqltype.Integer(integer, 1)
	negative, _ := sqltype.Integer(integer, -1)
	plan, err := sqltype.BindString("substring", text.Usage(), text.Usage(), []sqltype.Usage{text.Usage(), integer.Usage(), integer.Usage()}, [4]bool{true}, false)
	if err != nil {
		t.Fatal(err)
	}
	var order []int
	node := String[stringUnitExpression, *stringUnitContext, struct{}]{Info: Info[struct{}]{Usage: text.Usage()}, Plan: plan, Textual: [4]bool{true}, Syntax: parser.SubstringStringSyntax, Args: []stringUnitExpression{
		stringUnitArgument{Info: Info[struct{}]{Usage: text.Usage()}, value: sqltype.Null(text), order: &order, index: 0},
		stringUnitArgument{Info: Info[struct{}]{Usage: integer.Usage()}, value: one, order: &order, index: 1},
		stringUnitArgument{Info: Info[struct{}]{Usage: integer.Usage()}, value: negative, order: &order, index: 2},
	}}
	got, err := node.Eval(&stringUnitContext{Context: context.Background()})
	if err != nil || !got.IsNull() || !reflect.DeepEqual(order, []int{0, 1, 2}) {
		t.Fatal("strict runtime NULL lost child demand", got, order, err)
	}
	failure := errors.New("count child demanded")
	args := append([]stringUnitExpression(nil), node.Args...)
	args[2] = stringUnitArgument{Info: Info[struct{}]{Usage: integer.Usage()}, order: &order, index: 2, err: failure}
	rebuilt, err := node.Reconstruct(node.Info, args, text.Usage())
	if err != nil || rebuilt.Syntax != parser.SubstringStringSyntax {
		t.Fatal("lost reconstructed syntax", err)
	}
	order = nil
	if _, err := rebuilt.Eval(&stringUnitContext{Context: context.Background()}); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0, 1, 2}) {
		t.Fatal("reconstructed count not demanded", order, err)
	}
	if err := node.Validate(); err != nil {
		t.Fatal("original carrier invalidated", err)
	}
}
