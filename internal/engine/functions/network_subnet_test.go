package functions

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"testing"
)

func subnetUnit(t *testing.T, op sqltype.NetworkSubnetOperator) (NetworkSubnet, *stringUnitContext, *[]int) {
	t.Helper()
	types := inspectCarrierTypes()
	order := []int{}
	var args [2]expr.Expr
	for i, text := range []string{"192.168.1.5", "192.168.1.199/24"} {
		v, err := types[sqltype.Inet].Input(context.Background(), text, -1)
		if err != nil {
			t.Fatal(err)
		}
		args[i] = stringUnitArgument{Info: expr.Info{Usage: types[sqltype.Inet].Usage()}, value: v, order: &order, index: i}
	}
	n, err := NewNetworkSubnet(expr.Info{Usage: types[sqltype.Bool].Usage()}, op, args, "pg_catalog")
	if err != nil {
		t.Fatal(err)
	}
	return n, &stringUnitContext{Context: context.Background()}, &order
}
func TestNetworkSubnetProviderLifecycle(t *testing.T) {
	for _, op := range []sqltype.NetworkSubnetOperator{sqltype.NetworkSubnetSub, sqltype.NetworkSubnetSubeq, sqltype.NetworkSubnetSup, sqltype.NetworkSubnetSupeq, sqltype.NetworkSubnetOverlap} {
		n, c, order := subnetUnit(t, op)
		fresh, _, _ := subnetUnit(t, op)
		if n.ValidateStoredProvider() != nil {
			t.Fatal("invalid provider")
		}
		if _, err := n.Eval(c); err != nil || !reflect.DeepEqual(*order, []int{0, 1}) {
			t.Fatal(*order, err)
		}
		rebuilt, err := n.Rebuild(fresh.Info, fresh.Children())
		if err != nil {
			t.Fatal(err)
		}
		next := rebuilt.(NetworkSubnet)
		if next.Validate() != nil || next.Plan == n.Plan {
			t.Fatal("stale authority")
		}
		if _, err = n.Reconstruct(n.Info, fresh.Args); err == nil {
			t.Fatal("foreign output")
		}
		if _, err = n.Rebuild(n.Info, nil); err == nil {
			t.Fatal("missing children")
		}
		bad := n
		bad.Args[0] = nil
		if bad.Validate() == nil {
			t.Fatal("missing operand")
		}
		bad = n
		bad.Usage = n.Args[0].ResultUsage()
		if bad.Validate() == nil {
			t.Fatal("wrong result")
		}
		rendering, err := n.Render()
		if err != nil || rendering.Form != expr.FormInfix || rendering.Namespace != "pg_catalog" || rendering.Replay.Validate == nil || rendering.Replay.Validate() != nil {
			t.Fatal(rendering, err)
		}
		captured, ok, err := n.Capture(expr.CaptureOperator)
		if err != nil || !ok || captured.Count != 2 {
			t.Fatal(captured, ok, err)
		}
		same := func(a, b expr.Expr) bool { return a.ResultUsage() == b.ResultUsage() }
		if !n.SameGroup(n, same, false) || n.SameGroup(fresh, same, false) {
			t.Fatal("group identity")
		}
		substituted, err := n.Substitute(n.Info, n.Children())
		if err != nil || substituted.(NetworkSubnet).Validate() != nil {
			t.Fatal(err)
		}
		// Row NULL does not hide evaluated child errors; literal NULL suppresses invocation.
		null := n.Args[0].(stringUnitArgument)
		null.value = sqltype.Null(null.Usage.Type)
		n.Args[0] = null
		failure := errors.New("subnet child")
		right := n.Args[1].(stringUnitArgument)
		right.err = failure
		n.Args[1] = right
		if _, err = n.Eval(c); !errors.Is(err, failure) {
			t.Fatal(err)
		}
		null.literal = true
		n.Args[0] = null
		*order = nil
		v, err := n.Eval(c)
		if err != nil || !v.IsNull() || v.Type() != n.Usage.Type || len(*order) != 0 {
			t.Fatal(v, err, *order)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		c.Context = ctx
		if _, err = n.Eval(c); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
}
