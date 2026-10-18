package functions

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestDatetimeOverlapsOnceOnlyChildrenAndErrorOrder(t *testing.T) {
	d := temporalTestTypes()
	plan, err := sqltype.BindDatetimeOverlaps(d[sqltype.Timestamp], d[sqltype.Interval], d[sqltype.Bool], d[sqltype.Time], [2]bool{}, [4]bool{})
	if err != nil {
		t.Fatal(err)
	}
	e := DatetimeOverlaps{Info: expr.Info{Usage: d[sqltype.Bool].Usage()}, Plan: plan}
	order := []int{}
	for i, n := range [...]int64{0, 2 * 86400000000, 86400000000, 3 * 86400000000} {
		value := temporalTestTimestamp(t, d[sqltype.Timestamp], n)
		e.Args[i] = temporalTestArgument{Info: expr.Info{Usage: value.Type().Usage()}, value: value, order: &order, index: i}
	}
	first := e.Args[0].(temporalTestArgument)
	first.value = sqltype.Null(first.ResultUsage().Type)
	e.Args[0] = first
	got, err := e.Eval(&temporalTestContext{Context: context.Background()})
	truth, failure := got.Boolean()
	if err != nil || failure != nil || !truth || !reflect.DeepEqual(order, []int{0, 1, 2, 3}) {
		t.Fatal(got, order, err, failure)
	}
	failure = errors.New("fourth reached child")
	last := e.Args[3].(temporalTestArgument)
	last.err = failure
	e.Args[3] = last
	order = nil
	if _, err := e.Eval(&temporalTestContext{Context: context.Background()}); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0, 1, 2, 3}) {
		t.Fatal(order, err)
	}
	childDiagnostic := &sqlstate.Error{Code: "22012", Message: "division by zero"}
	last.err = childDiagnostic
	e.Args[3] = last
	order = nil
	e.WrapperContext = true
	if _, err := e.Eval(&temporalTestContext{Context: context.Background()}); err != childDiagnostic || childDiagnostic.Context != "" || !reflect.DeepEqual(order, []int{0, 1, 2, 3}) {
		t.Fatal("child diagnostic altered", order, err)
	}
}
