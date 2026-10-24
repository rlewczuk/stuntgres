package functions

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"testing"
)

type castRoutineUnitContext struct{ stringUnitContext }

func (*castRoutineUnitContext) FunctionOutput() sqltype.OutputEnvironment {
	return sqltype.OutputEnvironment{}
}

func TestCastRoutineProviderLifecycle(t *testing.T) {
	types := inspectCarrierTypes()
	order := []int{}
	argument := func(id sqltype.ID, text string, index int) expr.Expr {
		d := types[id]
		v, err := d.Input(context.Background(), text, -1)
		if err != nil {
			t.Fatal(err)
		}
		return stringUnitArgument{Info: expr.Info{Usage: d.Usage()}, value: v, order: &order, index: index}
	}
	args := []expr.Expr{argument(sqltype.Int4, "5", 0), argument(sqltype.Int4, "4", 1)}
	node, err := NewCastRoutine(expr.Info{Usage: types[sqltype.Bit].Usage()}, "bit", "pg_catalog", args, expr.Immutable)
	if err != nil {
		t.Fatal(err)
	}
	value, err := node.Eval(&castRoutineUnitContext{stringUnitContext{Context: context.Background()}})
	if err != nil {
		t.Fatal(err)
	}
	text, err := value.Type().Output(context.Background(), value)
	if err != nil || text != "0101" || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatalf("value=%q order=%v error=%v", text, order, err)
	}
	if err := node.ValidateStoredProvider(); err != nil {
		t.Fatal(err)
	}
	capture, ok, err := node.Capture(expr.CaptureFunction)
	if err != nil || !ok || capture.Check(node, 2) != nil {
		t.Fatal("capture", ok, err)
	}
	if _, err := node.Render(); err != nil {
		t.Fatal(err)
	}
	// A foreign formal cannot silently select a different conversion kernel.
	foreign := []expr.Expr{argument(sqltype.Text, "5", 0), args[1]}
	replaced, err := node.Substitute(node.Info, foreign)
	if err != nil {
		t.Fatal(err)
	}
	if replaced.(CastRoutine).ValidateStoredProvider() == nil {
		t.Fatal("foreign formal accepted")
	}
	rebuilt, err := node.Rebuild(node.Info, node.Children())
	if err != nil || !node.SameGroup(rebuilt, func(a, b expr.Expr) bool { return a == b }, false) {
		t.Fatal("rebuild", err)
	}
}
