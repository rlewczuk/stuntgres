package functions

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestDatetimeMakeDeclaredSlotDemand(t *testing.T) {
	ctx := context.Background()
	d := temporalTestTypes()
	plan, err := sqltype.BindDatetimeMake("make_interval", d[sqltype.Interval], 7)
	if err != nil {
		t.Fatal(err)
	}
	e := DatetimeMake{Info: expr.Info{Usage: d[sqltype.Interval].Usage()}, Plan: plan, Count: 7}
	for i := range e.Args {
		var v sqltype.Value
		if i == 6 {
			v, err = sqltype.Float(d[sqltype.Float8], 0)
		} else {
			v, err = sqltype.Integer(d[sqltype.Int4], 0)
		}
		if err != nil {
			t.Fatal(err)
		}
		e.Args[i] = temporalTestLiteral(v)
	}
	var order []int
	e.Args[3] = temporalTestArgument{Info: expr.Info{Usage: d[sqltype.Int4].Usage()}, value: sqltype.Null(d[sqltype.Int4]), order: &order, index: 3}
	failure := errors.New("demanded seconds")
	e.Args[6] = temporalTestArgument{Info: expr.Info{Usage: d[sqltype.Float8].Usage()}, order: &order, index: 6, err: failure}
	if _, err := e.Eval(&temporalTestContext{Context: ctx}); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{3, 6}) {
		t.Fatal(order, err)
	}
}
