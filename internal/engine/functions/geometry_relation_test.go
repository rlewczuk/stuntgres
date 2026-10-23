package functions

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"testing"
)

func relationProviderUnit(t *testing.T, name string, left, right sqltype.ID, a, b string) (GeometryRelation, *stringUnitContext, *[]int) {
	t.Helper()
	types := inspectCarrierTypes()
	op, ok := sqltype.GeometryRelationKind(name, left, right)
	if !ok {
		t.Fatal("missing operator")
	}
	order := []int{}
	var args [2]expr.Expr
	for i, id := range [2]sqltype.ID{left, right} {
		text := a
		if i == 1 {
			text = b
		}
		value, err := types[id].Input(context.Background(), text, -1)
		if err != nil {
			t.Fatal(err)
		}
		args[i] = stringUnitArgument{Info: expr.Info{Usage: types[id].Usage()}, value: value, order: &order, index: i}
	}
	n, err := NewGeometryRelation(expr.Info{Usage: types[op.ResultID()].Usage()}, op, args, "pg_catalog")
	if err != nil {
		t.Fatal(err)
	}
	return n, &stringUnitContext{Context: context.Background()}, &order
}
func TestGeometryRelationProviderLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name        string
		left, right sqltype.ID
		a, b        string
	}{
		{"##", sqltype.Point, sqltype.Lseg, "(1,1)", "[(0,0),(2,0)]"},
		{"#", sqltype.Box, sqltype.Box, "(2,2),(0,0)", "(1,1),(0,0)"},
		{"<->", sqltype.Point, sqltype.Line, "(1,1)", "{0,1,0}"},
		{"@>", sqltype.Polygon, sqltype.Polygon, "((0,0),(2,0),(2,2),(0,2))", "((1,1))"},
	} {
		n, c, order := relationProviderUnit(t, tc.name, tc.left, tc.right, tc.a, tc.b)
		fresh, _, _ := relationProviderUnit(t, tc.name, tc.left, tc.right, tc.a, tc.b)
		if err := n.ValidateStoredProvider(); err != nil {
			t.Fatal(err)
		}
		if v, err := n.Eval(c); err != nil || v.IsNull() || v.Type() != n.Usage.Type || !reflect.DeepEqual(*order, []int{0, 1}) {
			t.Fatal("evaluation/result/order", v, err, *order)
		}
		next, err := n.Rebuild(fresh.Info, fresh.Children())
		if err != nil || next.(GeometryRelation).Validate() != nil || next.(GeometryRelation).Plan == n.Plan {
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
		if bad.ValidateStoredProvider() == nil {
			t.Fatal("missing stored operand admitted")
		}
		substituted, err := n.Substitute(n.Info, n.Children())
		if err != nil || substituted.(GeometryRelation).Validate() != nil {
			t.Fatal("substitution", err)
		}
		if _, err := n.Substitute(n.Info, n.Children()[:1]); err == nil {
			t.Fatal("short substitution")
		}
		rendering, err := n.Render()
		if err != nil || rendering.Form != expr.FormInfix || rendering.Namespace != "pg_catalog" || !rendering.Replay.QuoteNamespace || rendering.Replay.Validate == nil || rendering.Replay.Validate() != nil {
			t.Fatal("stored replay", rendering, err)
		}
		for i, arg := range rendering.Args {
			if arg.Cast != n.Args[i].ResultUsage() || arg.Transport != expr.TransportCast || arg.TransportType != n.Args[i].ResultUsage() {
				t.Fatal("lost formal transport", i, arg)
			}
		}
		capture, ok, err := n.Capture(expr.CaptureOperator)
		if err != nil || !ok || capture.Count != 2 || !reflect.DeepEqual(capture.Inline, n.Args) {
			t.Fatal("operator capture", capture, ok, err)
		}
		if _, ok, err := n.Capture(expr.CaptureFunction); ok || err != nil {
			t.Fatal("callable capture admitted", ok, err)
		}
		same := func(a, b expr.Expr) bool { return a.ResultUsage() == b.ResultUsage() }
		if !n.SameGroup(n, same, false) || n.SameGroup(fresh, same, false) || n.LooksLikeFunction() || n.IntrinsicVolatility() != expr.Immutable {
			t.Fatal("group/provider properties")
		}
		qualified := n
		qualified.Namespace = "other"
		if n.SameGroup(qualified, same, false) {
			t.Fatal("namespace group lost")
		}
		n.ClosureTransparent()
		// Runtime row NULL must not suppress a demanded later child error.
		null := n.Args[0].(stringUnitArgument)
		null.value = sqltype.Null(null.Usage.Type)
		n.Args[0] = null
		failure := errors.New("geometry relation child")
		right := n.Args[1].(stringUnitArgument)
		right.err = failure
		n.Args[1] = right
		if _, err := n.Eval(c); !errors.Is(err, failure) {
			t.Fatal("row NULL hid demanded child", err)
		}
		null.literal = true
		n.Args[0] = null
		*order = nil
		if v, err := n.Eval(c); err != nil || !v.IsNull() || v.Type() != n.Usage.Type || len(*order) != 0 {
			t.Fatal("literal NULL strict shortcut", v, err, *order)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		c.Context = ctx
		if _, err := n.Eval(c); !errors.Is(err, context.Canceled) {
			t.Fatal("literal NULL hid cancellation", err)
		}
	}
}
