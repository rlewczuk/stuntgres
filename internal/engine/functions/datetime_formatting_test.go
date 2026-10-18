package functions

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestDatetimeFormattingChildDemand(t *testing.T) {
	ctx := context.Background()
	d := temporalTestTypes()
	source, text := d[sqltype.Timestamp], d[sqltype.Text]
	template := "YYYY"
	plan, err := sqltype.BindDatetimeFormat(ctx, "to_char", text.Usage(), source.Usage(), &template)
	if err != nil {
		t.Fatal(err)
	}
	var order []int
	node := DatetimeFormatting{Info: expr.Info{Usage: text.Usage()}, Plan: plan, Args: [2]expr.Expr{
		temporalTestArgument{Info: expr.Info{Usage: source.Usage()}, value: sqltype.Null(source), order: &order, index: 0},
		temporalTestArgument{Info: expr.Info{Usage: text.Usage()}, value: sqltype.String(text, template), order: &order, index: 1},
	}}
	value, err := node.Eval(&temporalTestContext{Context: ctx})
	if err != nil || !value.IsNull() || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal("row NULL suppressed demanded sibling", value, order, err)
	}
	failure := errors.New("demanded formatting sibling")
	node.Args[1] = temporalTestArgument{Info: expr.Info{Usage: text.Usage()}, order: &order, index: 1, err: failure}
	order = nil
	if _, err := node.Eval(&temporalTestContext{Context: ctx}); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal(order, err)
	}
}
