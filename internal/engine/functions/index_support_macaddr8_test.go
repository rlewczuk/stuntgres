package functions

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func TestMACADDR8CompareSupportLifecycle(t *testing.T) {
	types := inspectCarrierTypes()
	order := []int{}
	var args [2]expr.Expr
	for i, bits := range []uint64{0x7fffffffffffffff, 0x8000000000000000} {
		value, err := sqltype.MACADDR8Value(types[sqltype.Macaddr8], bits)
		if err != nil {
			t.Fatal(err)
		}
		args[i] = stringUnitArgument{Info: expr.Info{Usage: types[sqltype.Macaddr8].Usage()}, value: value, order: &order, index: i}
	}
	info := expr.Info{Usage: types[sqltype.Int4].Usage(), RoutineReference: macaddr8UnitRoutine(4119)}
	node, err := NewMacaddr8Compare(info, args)
	if err != nil {
		t.Fatal(err)
	}
	ctx := &stringUnitContext{Context: context.Background()}
	value, err := node.Eval(ctx)
	if err != nil || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal("each operand once in order", order, err)
	}
	if sign, err := value.Integer(); err != nil || sign != -1 {
		t.Fatal("unsigned comparator sign", sign, err)
	}
	if node.ValidateStoredProvider() != nil {
		t.Fatal("valid provider rejected")
	}
	for _, rebuild := range []func(expr.Info, []expr.Expr) (expr.Expr, error){node.Rebuild, node.Substitute} {
		next, err := rebuild(info, node.Children())
		if err != nil {
			t.Fatal(err)
		}
		current := next.(Macaddr8Compare)
		if !current.comparison.Equal(node.comparison) || current.CallRoutineReference() != info.RoutineReference || current.ValidateStoredProvider() != nil {
			t.Fatal("same-source reconstruction lost captured authority")
		}
		if _, err := rebuild(info, nil); err == nil {
			t.Fatal("missing children admitted")
		}
	}
	fresh := inspectCarrierTypes()
	freshArgs := args
	for i, arg := range args {
		a := arg.(stringUnitArgument)
		a.Usage = fresh[sqltype.Macaddr8].Usage()
		a.value, err = sqltype.MACADDR8Value(a.Usage.Type, uint64(i))
		if err != nil {
			t.Fatal(err)
		}
		freshArgs[i] = a
	}
	freshInfo := info
	freshInfo.Usage = fresh[sqltype.Int4].Usage()
	rebuilt, err := node.Rebuild(freshInfo, freshArgs[:])
	if err != nil {
		t.Fatal(err)
	}
	destination := rebuilt.(Macaddr8Compare)
	if destination.comparison.Equal(node.comparison) || destination.ValidateStoredProvider() != nil {
		t.Fatal("destination retained source comparison generation")
	}
	bad := node
	bad.Args = freshArgs
	if bad.ValidateStoredProvider() == nil {
		t.Fatal("foreign operands retained old comparison")
	}
	bad = node
	bad.Usage = types[sqltype.Bool].Usage()
	if bad.ValidateStoredProvider() == nil {
		t.Fatal("wrong result admitted")
	}
	bad = node
	bad.Args[0] = nil
	if bad.ValidateStoredProvider() == nil {
		t.Fatal("missing operand admitted")
	}
	rendering, err := node.Render()
	if err != nil || rendering.Name != "macaddr8_cmp" || rendering.Namespace != "pg_catalog" || rendering.Replay.Validate == nil || rendering.Replay.Validate() != nil {
		t.Fatal("rendered routine identity", rendering, err)
	}
	capture, ok, err := node.Capture(expr.CaptureFunction)
	if err != nil || !ok || capture.Count != 2 || !reflect.DeepEqual(capture.Inline, args) {
		t.Fatal("operand capture", capture, ok, err)
	}
	if _, ok, err := node.Capture(expr.CaptureOperator); err != nil || ok {
		t.Fatal("invented operator capture", ok, err)
	}
	same := func(a, b expr.Expr) bool { return a.ResultUsage() == b.ResultUsage() }
	if !node.SameGroup(node, same, false) || node.SameGroup(destination, same, false) {
		t.Fatal("bound comparison group identity")
	}
	left := args[0].(stringUnitArgument)
	left.value = sqltype.Null(left.Usage.Type)
	node.Args[0] = left
	order = nil
	value, err = node.Eval(ctx)
	if err != nil || !value.IsNull() || value.Type() != types[sqltype.Int4] || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal("strict row NULL", value, order, err)
	}
	right := args[1].(stringUnitArgument)
	failure := errors.New("MACADDR8 cmp child")
	right.err = failure
	node.Args[1] = right
	if _, err := node.Eval(ctx); !errors.Is(err, failure) {
		t.Fatal("row NULL hid child error", err)
	}
	left.literal = true
	node.Args[0] = left
	order = nil
	value, err = node.Eval(ctx)
	if err != nil || !value.IsNull() || len(order) != 0 {
		t.Fatal("literal NULL demanded children", order, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	ctx.Context = canceled
	if _, err := node.Eval(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("comparison cancellation", err)
	}
}
