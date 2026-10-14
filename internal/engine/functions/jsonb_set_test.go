package functions

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/sqltype"
)

func TestJSONBSetAllArgumentDemand(t *testing.T) {
	d := temporalTestTypes()
	ctx := &temporalTestContext{Context: context.Background()}
	usages := []sqltype.Usage{d[sqltype.JSONB].Usage(), d[1009].Usage(), d[sqltype.JSONB].Usage(), d[sqltype.Bool].Usage(), d[sqltype.Text].Usage()}
	plan, err := sqltype.BindJSONBSet("jsonb_set_lax", d[sqltype.JSONB].Usage(), usages...)
	if err != nil {
		t.Fatal(err)
	}
	base := JSONBSet[temporalTestExpression, *temporalTestContext, struct{}]{Info: Info[struct{}]{Usage: d[sqltype.JSONB].Usage()}, Plan: plan, Count: 5, WrittenCount: 5, WrittenSlots: [5]uint8{4, 2, 0, 3, 1}, Labels: [5]string{"null_value_treatment", "replacement", "jsonb_in", "create_if_missing", "path"}}
	for i := range base.Count {
		base.Args[i] = temporalTestLiteral(sqltype.Null(usages[i].Type))
	}
	for failing := range 5 {
		e := base
		failure := errors.New("ordinary child failure")
		var order []int
		for i := range e.Count {
			e.Args[i] = temporalTestArgument{Info: Info[struct{}]{Usage: usages[i]}, value: sqltype.Null(usages[i].Type), order: &order, index: i}
		}
		arg := e.Args[failing].(temporalTestArgument)
		arg.err = failure
		e.Args[failing] = arg
		got, err := e.Eval(ctx)
		var want []int
		for _, slot := range e.WrittenSlots {
			want = append(want, int(slot))
			if int(slot) == failing {
				break
			}
		}
		if !errors.Is(err, failure) || got != (sqltype.Value{}) || !reflect.DeepEqual(order, want) {
			t.Fatal("argument suppressed/reordered", failing, order, want, got, err)
		}
	}
	got, err := base.Eval(ctx)
	if err != nil || !got.IsNull() || got.Type() != d[sqltype.JSONB] {
		t.Fatal(got, err)
	}
	// Strict set must likewise evaluate the other children after a NULL child.
	strict, err := sqltype.BindJSONBSet("jsonb_set", d[sqltype.JSONB].Usage(), usages[:4]...)
	if err != nil {
		t.Fatal(err)
	}
	e := base
	e.Plan = strict
	e.Count = 4
	e.WrittenCount = 4
	e.WrittenSlots = [5]uint8{0, 1, 2, 3}
	failure := errors.New("strict NULL does not hide child error")
	e.Args[3] = temporalTestArgument{Info: Info[struct{}]{Usage: usages[3]}, err: failure}
	if got, err := e.Eval(ctx); !errors.Is(err, failure) || got != (sqltype.Value{}) {
		t.Fatal(got, err)
	}
}

func TestJSONBSetReconstructAuthorityAndPresentation(t *testing.T) {
	d, fresh := temporalTestTypes(), temporalTestTypes()
	ids := [5]sqltype.ID{sqltype.JSONB, 1009, sqltype.JSONB, sqltype.Bool, sqltype.Text}
	var usages [5]sqltype.Usage
	e := JSONBSet[temporalTestExpression, *temporalTestContext, struct{}]{Info: Info[struct{}]{Usage: d[sqltype.JSONB].Usage()}, Count: 5, WrittenCount: 3, WrittenSlots: [5]uint8{2, 0, 1}, Labels: [5]string{"replacement", "jsonb_in", "path"}, Variadic: true}
	for i, id := range ids {
		usages[i] = d[id].Usage()
		e.Args[i] = temporalTestLiteral(sqltype.Null(d[id]))
	}
	plan, err := sqltype.BindJSONBSet("jsonb_set_lax", e.Usage, usages[:]...)
	if err != nil {
		t.Fatal(err)
	}
	e.Plan = plan
	var args [5]temporalTestExpression
	for i, id := range ids {
		args[i] = temporalTestLiteral(sqltype.Null(fresh[id]))
	}
	got, err := e.Reconstruct(Info[struct{}]{Usage: fresh[sqltype.JSONB].Usage()}, args)
	if err != nil || got.Usage.Type != fresh[sqltype.JSONB] || got.WrittenSlots != e.WrittenSlots || got.Labels != e.Labels || !got.Variadic || got.WrittenCount != 3 {
		t.Fatal(got, err)
	}
	for i, id := range ids {
		if got.Args[i].ResultUsage().Type != fresh[id] || e.Args[i].ResultUsage().Type != d[id] {
			t.Fatal("reconstruction mutated source", i)
		}
	}
	bad := args
	bad[4] = e.Args[4]
	if _, err := e.Reconstruct(Info[struct{}]{Usage: fresh[sqltype.JSONB].Usage()}, bad); err == nil {
		t.Fatal("foreign mode generation accepted")
	}
}
