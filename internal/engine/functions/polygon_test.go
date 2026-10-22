package functions

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func polygonProviderUnit(t *testing.T, op sqltype.PolygonOperator, function bool) (Polygon, *stringUnitContext, *[]int) {
	t.Helper()
	types := inspectCarrierTypes()
	left, right := op.Signature()
	order := []int{}
	var args [2]expr.Expr
	for i, id := range [2]sqltype.ID{left, right} {
		text := "0,0,4,0,0,4"
		if id == sqltype.Point {
			text = "(1,1)"
		}
		v, err := types[id].Input(context.Background(), text, -1)
		if err != nil {
			t.Fatal(err)
		}
		args[i] = stringUnitArgument{Info: expr.Info{Usage: types[id].Usage()}, value: v, order: &order, index: i}
	}
	n, err := NewPolygon(expr.Info{Usage: types[sqltype.Bool].Usage()}, op, args, "pg_catalog", function)
	if err != nil {
		t.Fatal(err)
	}
	return n, &stringUnitContext{Context: context.Background()}, &order
}
func TestPolygonProviderLifecycle(t *testing.T) {
	for _, op := range []sqltype.PolygonOperator{sqltype.PolygonSame, sqltype.PolygonContainsPoint, sqltype.PointContainedPolygon} {
		for _, function := range []bool{false, true} {
			n, c, order := polygonProviderUnit(t, op, function)
			fresh, _, _ := polygonProviderUnit(t, op, function)
			if err := n.ValidateStoredProvider(); err != nil {
				t.Fatal(err)
			}
			if v, err := n.Eval(c); err != nil || v.IsNull() || !reflect.DeepEqual(*order, []int{0, 1}) {
				t.Fatal(v, err, *order)
			}
			next, err := n.Rebuild(fresh.Info, fresh.Children())
			if err != nil || next.(Polygon).Validate() != nil || next.(Polygon).Plan == n.Plan {
				t.Fatal("rebuild authority", err)
			}
			if _, err := n.Rebuild(n.Info, fresh.Children()); err == nil {
				t.Fatal("foreign result")
			}
			if _, err := n.Rebuild(n.Info, nil); err == nil {
				t.Fatal("missing children")
			}
			bad := n
			bad.Args[0] = nil
			if bad.Validate() == nil {
				t.Fatal("missing operand")
			}
			rendering, err := n.Render()
			if err != nil || rendering.Namespace != "pg_catalog" || rendering.Replay.Validate == nil || rendering.Replay.Validate() != nil {
				t.Fatal(rendering, err)
			}
			if !function && rendering.Form != expr.FormInfix {
				t.Fatal("operator replay", rendering)
			}
			kind := expr.CaptureOperator
			if function {
				kind = expr.CaptureFunction
			}
			capture, ok, err := n.Capture(kind)
			if err != nil || !ok || capture.Count != 2 {
				t.Fatal(capture, ok, err)
			}
			other := expr.CaptureFunction
			if function {
				other = expr.CaptureOperator
			}
			if _, ok, err := n.Capture(other); ok || err != nil {
				t.Fatal("wrong capture", ok, err)
			}
			same := func(a, b expr.Expr) bool { return a.ResultUsage() == b.ResultUsage() }
			if !n.SameGroup(n, same, false) || n.SameGroup(fresh, same, false) {
				t.Fatal("group authority")
			}
			substitute, err := n.Substitute(n.Info, n.Children())
			if err != nil || substitute.(Polygon).Validate() != nil {
				t.Fatal(err)
			}
			null := n.Args[0].(stringUnitArgument)
			null.value = sqltype.Null(null.Usage.Type)
			n.Args[0] = null
			failure := errors.New("polygon child")
			right := n.Args[1].(stringUnitArgument)
			right.err = failure
			n.Args[1] = right
			if _, err := n.Eval(c); !errors.Is(err, failure) {
				t.Fatal("row NULL hid child", err)
			}
			null.literal = true
			n.Args[0] = null
			*order = nil
			if v, err := n.Eval(c); err != nil || !v.IsNull() || v.Type() != n.Usage.Type || len(*order) != 0 {
				t.Fatal(v, err, *order)
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
