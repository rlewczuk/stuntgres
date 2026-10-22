package functions

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func pointProviderUnit(t *testing.T, op sqltype.PointOperator, function bool) (Point, *stringUnitContext, *[]int) {
	t.Helper()
	types := inspectCarrierTypes()
	order := []int{}
	var args [2]expr.Expr
	for i, text := range []string{"0,0", "2,3"} {
		v, err := types[sqltype.Point].Input(context.Background(), text, -1)
		if err != nil {
			t.Fatal(err)
		}
		args[i] = stringUnitArgument{Info: expr.Info{Usage: types[sqltype.Point].Usage()}, value: v, order: &order, index: i}
	}
	n, err := NewPoint(expr.Info{Usage: types[sqltype.Bool].Usage()}, op, args, "pg_catalog", function)
	if err != nil {
		t.Fatal(err)
	}
	return n, &stringUnitContext{Context: context.Background()}, &order
}

func TestPointProviderLifecycle(t *testing.T) {
	for op := sqltype.PointSame; op <= sqltype.PointVertical; op++ {
		for _, function := range []bool{false, true} {
			n, c, order := pointProviderUnit(t, op, function)
			fresh, _, _ := pointProviderUnit(t, op, function)
			if err := n.ValidateStoredProvider(); err != nil {
				t.Fatal(err)
			}
			if _, err := n.Eval(c); err != nil || !reflect.DeepEqual(*order, []int{0, 1}) {
				t.Fatal("evaluation order", *order, err)
			}
			next, err := n.Rebuild(fresh.Info, fresh.Children())
			if err != nil || next.(Point).Validate() != nil || next.(Point).Plan == n.Plan {
				t.Fatal("rebuild authority", err)
			}
			if _, err := n.Rebuild(n.Info, fresh.Children()); err == nil {
				t.Fatal("foreign result admitted")
			}
			if _, err := n.Rebuild(n.Info, nil); err == nil {
				t.Fatal("missing children admitted")
			}
			bad := n
			bad.Args[0] = nil
			if bad.Validate() == nil {
				t.Fatal("missing operand admitted")
			}
			bad = n
			bad.Usage = n.Args[0].ResultUsage()
			if bad.Validate() == nil {
				t.Fatal("wrong result admitted")
			}
			rendering, err := n.Render()
			if err != nil || rendering.Namespace != "pg_catalog" || rendering.Replay.Validate == nil || rendering.Replay.Validate() != nil {
				t.Fatal("rendering", rendering, err)
			}
			if !function && rendering.Form != expr.FormInfix {
				t.Fatal("infix replay", rendering)
			}
			kind := expr.CaptureOperator
			if function {
				kind = expr.CaptureFunction
			}
			capture, ok, err := n.Capture(kind)
			if err != nil || !ok || capture.Count != 2 {
				t.Fatal("capture", capture, ok, err)
			}
			otherKind := expr.CaptureFunction
			if function {
				otherKind = expr.CaptureOperator
			}
			if _, ok, err := n.Capture(otherKind); err != nil || ok {
				t.Fatal("wrong capture", ok, err)
			}
			same := func(a, b expr.Expr) bool { return a.ResultUsage() == b.ResultUsage() }
			if !n.SameGroup(n, same, false) || n.SameGroup(fresh, same, false) {
				t.Fatal("group expression identity")
			}
			substitute, err := n.Substitute(n.Info, n.Children())
			if err != nil || substitute.(Point).Validate() != nil {
				t.Fatal("substitute", err)
			}
			null := n.Args[0].(stringUnitArgument)
			null.value = sqltype.Null(null.Usage.Type)
			n.Args[0] = null
			failure := errors.New("point child")
			right := n.Args[1].(stringUnitArgument)
			right.err = failure
			n.Args[1] = right
			if _, err := n.Eval(c); !errors.Is(err, failure) {
				t.Fatal("row NULL hid child error", err)
			}
			null.literal = true
			n.Args[0] = null
			*order = nil
			v, err := n.Eval(c)
			if err != nil || !v.IsNull() || v.Type() != n.Usage.Type || len(*order) != 0 {
				t.Fatal("literal NULL", v, err, *order)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			c.Context = ctx
			if _, err := n.Eval(c); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		}
	}
}
