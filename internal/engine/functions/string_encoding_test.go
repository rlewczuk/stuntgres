package functions

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestStringEncodingRuntimeDemand(t *testing.T) {
	var text, name *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		switch d.ID() {
		case sqltype.Text:
			text = d
		case sqltype.Name:
			name = d
		}
	}
	plan, err := sqltype.BindString("to_ascii", text.Usage(), text.Usage(), []sqltype.Usage{text.Usage(), name.Usage()}, [4]bool{true}, false)
	if err != nil {
		t.Fatal(err)
	}
	var order []int
	node := String[stringUnitExpression, *stringUnitContext, struct{}]{Info: Info[struct{}]{Usage: text.Usage()}, Plan: plan, Textual: [4]bool{true}, Args: []stringUnitExpression{
		stringUnitArgument{Info: Info[struct{}]{Usage: text.Usage()}, value: sqltype.Null(text), order: &order, index: 0},
		stringUnitArgument{Info: Info[struct{}]{Usage: name.Usage()}, value: sqltype.String(name, "nope"), order: &order, index: 1},
	}}
	value, err := node.Eval(&stringUnitContext{Context: context.Background()})
	if err != nil || !value.IsNull() || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal("once-analyzed children", value, order, err)
	}
	failure := errors.New("demanded encoding child")
	node.Args = append([]stringUnitExpression(nil), node.Args...)
	node.Args[1] = stringUnitArgument{Info: Info[struct{}]{Usage: name.Usage()}, order: &order, index: 1, err: failure}
	order = nil
	if _, err := node.Eval(&stringUnitContext{Context: context.Background()}); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal("NULL suppressed child error", order, err)
	}
}
