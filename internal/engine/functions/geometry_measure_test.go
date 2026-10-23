package functions

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func TestGeometryMeasureGroupIdentity(t *testing.T) {
	types := inspectCarrierTypes()
	// Typed NULL inputs make every numeric result NULL, but do not make the
	// selected overload interchangeable in a grouped/stored expression.
	makeNode := func(op sqltype.GeometryMeasure, namespace string) GeometryMeasure {
		t.Helper()
		d := types[op.Formal(0)]
		arg := stringUnitArgument{Info: expr.Info{Usage: d.Usage()}, value: sqltype.Null(d)}
		n, err := NewGeometryMeasure(expr.Info{Usage: types[op.ResultID()].Usage()}, op, [2]expr.Expr{arg}, namespace)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	a := makeNode(sqltype.AreaBox, "pg_catalog")
	sameChildren := func(a, b expr.Expr) bool { return a.ResultUsage() == b.ResultUsage() }
	if !a.SameGroup(a, sameChildren, false) {
		t.Fatal("same measurement changed group")
	}
	for _, other := range []GeometryMeasure{
		makeNode(sqltype.AreaPath, "pg_catalog"),
		makeNode(sqltype.AreaCircle, "pg_catalog"),
		makeNode(sqltype.AreaBox, "other"),
	} {
		if a.SameGroup(other, sameChildren, false) {
			t.Fatal("different selected call merged")
		}
	}
	center := makeNode(sqltype.CenterBox, "pg_catalog")
	conversion, err := NewGeometryConversion(center.Info, sqltype.PointBox, center.Args, "pg_catalog")
	if err != nil {
		t.Fatal(err)
	}
	if center.SameGroup(conversion, sameChildren, false) {
		t.Fatal("center and point conversion merged")
	}
	rebuilt, err := a.Rebuild(a.Info, a.Children())
	if err != nil || !a.SameGroup(rebuilt, sameChildren, false) {
		t.Fatal("rebuild lost semantic group identity", err)
	}
}

func TestGeometryMeasureBoxLineProviderLifecycle(t *testing.T) {
	for op := sqltype.WidthBox; op <= sqltype.SlopePoints; op++ {
		t.Run(op.Name(), func(t *testing.T) {
			order := []int{}
			makeNode := func() GeometryMeasure {
				types := inspectCarrierTypes()
				var args [2]expr.Expr
				for i := range op.Arity() {
					d := types[op.Formal(i)]
					text := "(4,6),(1,2)"
					if op == sqltype.SlopePoints {
						text = "(0,0)"
						if i == 1 {
							text = "(2,1)"
						}
					}
					v, err := d.Input(context.Background(), text, -1)
					if err != nil {
						t.Fatal(err)
					}
					args[i] = stringUnitArgument{Info: expr.Info{Usage: d.Usage()}, value: v, order: &order, index: i}
				}
				n, err := NewGeometryMeasure(expr.Info{Usage: types[op.ResultID()].Usage()}, op, args, "pg_catalog")
				if err != nil {
					t.Fatal(err)
				}
				return n
			}
			n, fresh := makeNode(), makeNode()
			c := &stringUnitContext{Context: context.Background()}
			if n.ValidateStoredProvider() != nil || n.IntrinsicVolatility() != expr.Immutable || !n.LooksLikeFunction() {
				t.Fatal("provider contract")
			}
			if _, err := n.Eval(c); err != nil {
				t.Fatal(err)
			}
			wantOrder := []int{0}
			if op.Arity() == 2 {
				wantOrder = append(wantOrder, 1)
			}
			if !reflect.DeepEqual(order, wantOrder) {
				t.Fatal("actuals not evaluated once in child order", order)
			}
			rebuilt, err := n.Rebuild(fresh.Info, fresh.Children())
			if err != nil || rebuilt.(GeometryMeasure).Validate() != nil || rebuilt.(GeometryMeasure).Plan == n.Plan {
				t.Fatal("reconstruction authority", err)
			}
			if _, err := n.Rebuild(n.Info, fresh.Children()); err == nil {
				t.Fatal("foreign operands admitted")
			}
			if _, err := n.Rebuild(n.Info, nil); err == nil {
				t.Fatal("missing children admitted")
			}
			if _, err := n.Substitute(n.Info, nil); err == nil {
				t.Fatal("missing substitution admitted")
			}
			sub, err := n.Substitute(n.Info, n.Children())
			if err != nil || sub.(GeometryMeasure).Validate() != nil {
				t.Fatal("substitution", err)
			}
			spec, err := n.Render()
			if err != nil || spec.Form != expr.FormCall || spec.Replay.Validate == nil || spec.Replay.Validate() != nil {
				t.Fatal("typed replay", err)
			}
			captured, ok, err := n.Capture(expr.CaptureFunction)
			if err != nil || !ok || captured.Count != op.Arity() {
				t.Fatal("capture", captured, err)
			}
			for i := range op.Arity() {
				if captured.Inline[i] != n.Args[i] {
					t.Fatal("capture lost child", i)
				}
			}
			if _, ok, _ := n.Capture(expr.CaptureOperator); ok {
				t.Fatal("operator capture admitted")
			}
			same := func(a, b expr.Expr) bool {
				return a.ResultUsage() == b.ResultUsage() && a.(stringUnitArgument).index == b.(stringUnitArgument).index
			}
			if !n.SameGroup(n, same, false) || n.SameGroup(fresh, same, false) {
				t.Fatal("group authority")
			}
			if op.Arity() == 2 {
				changed := n
				right := changed.Args[1].(stringUnitArgument)
				right.index++
				changed.Args[1] = right
				if n.SameGroup(changed, same, false) {
					t.Fatal("second child omitted from group identity")
				}
				if _, err := n.Substitute(n.Info, []expr.Expr{n.Args[1], n.Args[0]}); err != nil {
					t.Fatal(err)
				}
			}
			broken := n
			broken.Args[op.Arity()-1] = nil
			if broken.Validate() == nil {
				t.Fatal("invalid provider admitted")
			}
			left := n.Args[0].(stringUnitArgument)
			left.value = sqltype.Null(left.Usage.Type)
			n.Args[0] = left
			if op.Arity() == 2 {
				right := n.Args[1].(stringUnitArgument)
				failure := errors.New("measurement second child")
				right.err = failure
				n.Args[1] = right
				order = nil
				if _, err := n.Eval(c); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0, 1}) {
					t.Fatal("runtime NULL hid second child", order, err)
				}
			}
			left.literal = true
			n.Args[0] = left
			order = nil
			got, err := n.Eval(c)
			if err != nil || !got.IsNull() || got.Type() != n.Usage.Type || len(order) != 0 {
				t.Fatal("literal NULL policy", got, err, order)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			c.Context = ctx
			if _, err := n.Eval(c); !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation", err)
			}
		})
	}
}
