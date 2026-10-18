package functions

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestStringCaseRuntimeDemand(t *testing.T) {
	var text *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Text {
			text = d
		}
	}
	usage := text.Usage()
	usage.Collation.ID = 811
	plan, err := sqltype.BindString("lower", usage, text.Usage(), []sqltype.Usage{usage}, [4]bool{true}, false)
	if err != nil {
		t.Fatal(err)
	}
	var order []int
	node := String{Info: expr.Info{Usage: usage}, Plan: plan, Textual: [4]bool{true}, Args: []expr.Expr{
		stringUnitArgument{Info: expr.Info{Usage: usage}, value: sqltype.String(text, "İ"), order: &order, index: 0},
	}}
	got, err := node.Eval(&stringUnitContext{Context: context.Background()})
	s, textErr := got.Text()
	if err != nil || textErr != nil || s != "i" || !reflect.DeepEqual(order, []int{0}) || node.ResultUsage() != usage || node.ResultUsage().Typmod != -1 {
		t.Fatal("once-only selected case", s, order, err, textErr)
	}
}
