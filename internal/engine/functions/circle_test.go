package functions

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"testing"
)

func circleProviderUnit(t *testing.T, op sqltype.CircleOperator) (Circle, *stringUnitContext, *[]int) {
	t.Helper()
	types := inspectCarrierTypes()
	order := []int{}
	var args [2]expr.Expr
	for i, text := range []string{"0,0,1", "5,0,1"} {
		if i == op.Arity() {
			break
		}
		v, err := types[sqltype.Circle].Input(context.Background(), text, -1)
		if err != nil {
			t.Fatal(err)
		}
		args[i] = stringUnitArgument{Info: expr.Info{Usage: types[sqltype.Circle].Usage()}, value: v, order: &order, index: i}
	}
	n, err := NewCircle(expr.Info{Usage: types[op.ResultID()].Usage()}, op, args, "pg_catalog", op == sqltype.CircleRadius)
	if err != nil {
		t.Fatal(err)
	}
	return n, &stringUnitContext{Context: context.Background()}, &order
}
func TestCircleProviderLifecycle(t *testing.T) {
	for op := sqltype.CircleEqual; op <= sqltype.CircleRadius; op++ {
		n, c, order := circleProviderUnit(t, op)
		fresh, _, _ := circleProviderUnit(t, op)
		if err := n.ValidateStoredProvider(); err != nil {
			t.Fatal(err)
		}
		wantOrder := []int{0}
		if op.Arity() == 2 {
			wantOrder = append(wantOrder, 1)
		}
		if _, err := n.Eval(c); err != nil || !reflect.DeepEqual(*order, wantOrder) {
			t.Fatal(op, *order, err)
		}
		next, err := n.Rebuild(fresh.Info, fresh.Children())
		if err != nil || next.(Circle).Validate() != nil || next.(Circle).Plan == n.Plan {
			t.Fatal("rebuild authority", op, err)
		}
		if _, err := n.Rebuild(n.Info, fresh.Children()); err == nil {
			t.Fatal("foreign result", op)
		}
		if _, err := n.Rebuild(n.Info, nil); err == nil {
			t.Fatal("missing children", op)
		}
		bad := n
		bad.Args[0] = nil
		if bad.Validate() == nil {
			t.Fatal("missing operand", op)
		}
		bad = n
		bad.Usage = n.Args[0].ResultUsage()
		if bad.Validate() == nil {
			t.Fatal("wrong result", op)
		}
		rendering, err := n.Render()
		if err != nil || rendering.Namespace != "pg_catalog" || rendering.Replay.Validate == nil || rendering.Replay.Validate() != nil {
			t.Fatal(rendering, err)
		}
		if !n.Function && rendering.Form != expr.FormInfix {
			t.Fatal("operator replay", rendering)
		}
		kind := expr.CaptureOperator
		if n.Function {
			kind = expr.CaptureFunction
		}
		capture, ok, err := n.Capture(kind)
		if err != nil || !ok || capture.Count != op.Arity() {
			t.Fatal(capture, ok, err)
		}
		same := func(a, b expr.Expr) bool { return a.ResultUsage() == b.ResultUsage() }
		if !n.SameGroup(n, same, false) || n.SameGroup(fresh, same, false) {
			t.Fatal("group expression identity", op)
		}
		substitute, err := n.Substitute(n.Info, n.Children())
		if err != nil || substitute.(Circle).Validate() != nil {
			t.Fatal(err)
		}
		null := n.Args[0].(stringUnitArgument)
		null.value = sqltype.Null(null.Usage.Type)
		n.Args[0] = null
		if op.Arity() == 2 {
			failure := errors.New("circle child")
			right := n.Args[1].(stringUnitArgument)
			right.err = failure
			n.Args[1] = right
			if _, err := n.Eval(c); !errors.Is(err, failure) {
				t.Fatal("row NULL hid child", err)
			}
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
