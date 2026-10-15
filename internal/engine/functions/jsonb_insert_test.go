package functions

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/sqltype"
)

func TestJSONBInsertWrittenDemandAndReconstruction(t *testing.T) {
	d, fresh := temporalTestTypes(), temporalTestTypes()
	ids := [4]sqltype.ID{sqltype.JSONB, 1009, sqltype.JSONB, sqltype.Bool}
	var usages [4]sqltype.Usage
	e := JSONBSet[temporalTestExpression, *temporalTestContext, struct{}]{Info: Info[struct{}]{Usage: d[sqltype.JSONB].Usage()}, Count: 4, WrittenCount: 4, WrittenSlots: [5]uint8{3, 2, 0, 1}, Labels: [5]string{"insert_after", "replacement", "jsonb_in", "path"}}
	for i, id := range ids {
		usages[i] = d[id].Usage()
		e.Args[i] = temporalTestLiteral(sqltype.Null(d[id]))
	}
	plan, err := sqltype.BindJSONBSet("jsonb_insert", e.Usage, usages[:]...)
	if err != nil {
		t.Fatal(err)
	}
	e.Plan = plan
	ctx := &temporalTestContext{Context: context.Background()}
	for failing := range 4 {
		n := e
		failure := errors.New("ordinary child error before strict NULL")
		var order []int
		for i := range 4 {
			arg := temporalTestArgument{Info: Info[struct{}]{Usage: usages[i]}, value: sqltype.Null(usages[i].Type), order: &order, index: i}
			if i == failing {
				arg.err = failure
			}
			n.Args[i] = arg
		}
		got, err := n.Eval(ctx)
		var want []int
		for _, slot := range e.WrittenSlots[:4] {
			want = append(want, int(slot))
			if int(slot) == failing {
				break
			}
		}
		if !errors.Is(err, failure) || got != (sqltype.Value{}) || !reflect.DeepEqual(order, want) {
			t.Fatal(failing, got, err, order, want)
		}
	}
	var args [5]temporalTestExpression
	for i, id := range ids {
		args[i] = temporalTestLiteral(sqltype.Null(fresh[id]))
	}
	r, err := e.Reconstruct(Info[struct{}]{Usage: fresh[sqltype.JSONB].Usage()}, args)
	if err != nil || r.WrittenSlots != e.WrittenSlots || r.Labels != e.Labels || r.WrittenCount != 4 || r.Plan.Name() != "jsonb_insert" {
		t.Fatal(r, err)
	}
	for i, id := range ids {
		if r.Args[i].ResultUsage().Type != fresh[id] || e.Args[i].ResultUsage().Type != d[id] {
			t.Fatal("reconstruction changed authority", i)
		}
	}
	got, err := r.Eval(ctx)
	if err != nil || !got.IsNull() || got.Type() != fresh[sqltype.JSONB] {
		t.Fatal(got, err)
	}
	for _, slot := range []int{0, 2} {
		bad := args
		bad[slot] = e.Args[slot]
		if _, err := e.Reconstruct(Info[struct{}]{Usage: fresh[sqltype.JSONB].Usage()}, bad); err == nil {
			t.Fatal("foreign JSONB generation accepted", slot)
		}
	}
}
