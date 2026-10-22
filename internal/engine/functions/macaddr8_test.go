package functions

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

type macaddr8UnitRoutine uint32

func (r macaddr8UnitRoutine) RoutineOID() uint32 { return uint32(r) }

func TestMACADDR8ComparisonProviderLifecycle(t *testing.T) {
	types := inspectCarrierTypes()
	order := []int{}
	var args [2]expr.Expr
	for i, bits := range []uint64{0x7fffffffffffffff, 0x8000000000000000} {
		v, err := sqltype.MACADDR8Value(types[sqltype.Macaddr8], bits)
		if err != nil {
			t.Fatal(err)
		}
		args[i] = stringUnitArgument{Info: expr.Info{Usage: types[sqltype.Macaddr8].Usage()}, value: v, order: &order, index: i}
	}
	info := expr.Info{Usage: types[sqltype.Bool].Usage(), RoutineReference: macaddr8UnitRoutine(4114)}
	n, err := newMACADDR8Comparison(info, "macaddr8_lt", args)
	if err != nil {
		t.Fatal(err)
	}
	c := &stringUnitContext{Context: context.Background()}
	v, err := n.Eval(c)
	if err != nil || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal("evaluation order", order, err)
	}
	if truth, err := v.Boolean(); err != nil || !truth {
		t.Fatal(v, err)
	}
	if err := n.ValidateStoredProvider(); err != nil {
		t.Fatal(err)
	}
	for _, reconstruct := range []func(expr.Info, []expr.Expr) (expr.Expr, error){n.Rebuild, n.Substitute} {
		next, err := reconstruct(info, n.Children())
		if err != nil {
			t.Fatal(err)
		}
		got := next.(MACADDR8Comparison)
		if got.Name != n.Name || got.Predicate != n.Predicate || got.CallRoutineReference() != info.RoutineReference || got.ValidateStoredProvider() != nil {
			t.Fatal("reconstruction authority", got)
		}
		if _, err := reconstruct(info, nil); err == nil {
			t.Fatal("missing children admitted")
		}
	}
	rendering, err := n.Render()
	if err != nil || rendering.Namespace != "pg_catalog" || rendering.Replay.Validate == nil || rendering.Replay.Validate() != nil {
		t.Fatal("rendering", rendering, err)
	}
	capture, ok, err := n.Capture(expr.CaptureFunction)
	if err != nil || !ok || capture.Count != 2 || !reflect.DeepEqual(capture.Inline, args) {
		t.Fatal("capture", capture, ok, err)
	}
	if _, ok, err := n.Capture(expr.CaptureOperator); err != nil || ok {
		t.Fatal("operator capture", ok, err)
	}
	same := func(a, b expr.Expr) bool { return a.ResultUsage() == b.ResultUsage() }
	if !n.SameGroup(n, same, false) {
		t.Fatal("group identity")
	}
	other, err := newMACADDR8Comparison(info, "macaddr8_ge", args)
	if err != nil || n.SameGroup(other, same, false) {
		t.Fatal("different callable identity", err)
	}
	bad := n
	bad.Name = "macaddr8_ge"
	if bad.ValidateStoredProvider() == nil {
		t.Fatal("mismatched predicate admitted")
	}
	bad = n
	bad.Args[0] = nil
	if bad.ValidateStoredProvider() == nil {
		t.Fatal("missing operand admitted")
	}
	bad = n
	bad.Usage = types[sqltype.Int4].Usage()
	if bad.ValidateStoredProvider() == nil {
		t.Fatal("wrong result admitted")
	}
	wrong := args
	wrong[0] = stringUnitArgument{Info: expr.Info{Usage: types[sqltype.Int4].Usage()}}
	if _, err := newMACADDR8Comparison(info, "macaddr8_lt", wrong); err == nil {
		t.Fatal("wrong operand admitted")
	}
	left := args[0].(stringUnitArgument)
	left.value = sqltype.Null(left.Usage.Type)
	n.Args[0] = left
	order = nil
	v, err = n.Eval(c)
	if err != nil || !v.IsNull() || v.Type() != types[sqltype.Bool] || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal("strict row NULL", v, order, err)
	}
	right := args[1].(stringUnitArgument)
	failure := errors.New("MACADDR8 child")
	right.err = failure
	n.Args[1] = right
	if _, err := n.Eval(c); !errors.Is(err, failure) {
		t.Fatal("row NULL hid child error", err)
	}
	left.literal = true
	n.Args[0] = left
	order = nil
	v, err = n.Eval(c)
	if err != nil || !v.IsNull() || len(order) != 0 {
		t.Fatal("literal NULL", v, order, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Context = ctx
	if _, err := n.Eval(c); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
