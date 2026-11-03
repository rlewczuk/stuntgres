package functions

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func TestFTSArrayConfigProviderLifecycle(t *testing.T) {
	types := inspectCarrierTypes()
	value, err := types[1009].Input(context.Background(), "{b,a,a}", -1)
	if err != nil {
		t.Fatal(err)
	}
	var order []int
	arg := stringUnitArgument{Info: expr.Info{Usage: types[1009].Usage()}, value: value, order: &order}
	n, err := NewFTSVector(expr.Info{Usage: types[sqltype.TSVector].Usage()}, sqltype.FTSArrayToVector, [3]expr.Expr{arg})
	if err != nil {
		t.Fatal(err)
	}
	if n.ValidateStoredProvider() != nil || n.IntrinsicVolatility() != expr.Immutable || n.PreflightFold() != expr.PreflightNullOrEvaluate {
		t.Fatal("constructor declaration")
	}
	for _, rebuild := range []func(expr.Info, []expr.Expr) (expr.Expr, error){n.Rebuild, n.Substitute} {
		next, err := rebuild(n.Info, n.Children())
		if err != nil || next.(FTSVector).Plan != n.Plan || !n.SameGroup(next, func(a, b expr.Expr) bool { return a == b }, true) {
			t.Fatal("constructor rebuild authority", err)
		}
		if _, err := rebuild(n.Info, nil); err == nil {
			t.Fatal("constructor lost its operand")
		}
	}
	capture, ok, err := n.Capture(expr.CaptureFunction)
	if err != nil || !ok || capture.Check(n, 1) != nil {
		t.Fatal("constructor capture", err)
	}
	if child, err := n.CaptureOperand(0); err != nil || child != arg {
		t.Fatal("constructor capture operand", err)
	}
	if _, err := n.CaptureOperand(1); err == nil {
		t.Fatal("constructor capture overflow")
	}
	spec, err := n.Render()
	if err != nil || spec.Namespace != "pg_catalog" || spec.Replay.Validate == nil || spec.Replay.Validate() != nil {
		t.Fatal("constructor symbolic replay", err)
	}
	c := &stringUnitContext{Context: context.Background()}
	out, err := n.Eval(c)
	if err != nil {
		t.Fatal(err)
	}
	text, err := out.Type().Output(context.Background(), out)
	if err != nil || text != "'a' 'b'" || len(order) != 1 {
		t.Fatal("constructor execution", text, order, err)
	}

	current := FTSCurrentConfig{Info: expr.Info{Usage: types[sqltype.Regconfig].Usage()}}
	if current.ValidateStoredProvider() != nil || current.IntrinsicVolatility() != expr.Stable || current.PlanningFold() != expr.FoldNever || current.PreflightFold() != expr.PreflightChildren {
		t.Fatal("stable current configuration folded")
	}
	for _, rebuild := range []func(expr.Info, []expr.Expr) (expr.Expr, error){current.Rebuild, current.Substitute} {
		next, err := rebuild(current.Info, nil)
		if err != nil || !current.SameGroup(next, nil, false) {
			t.Fatal("zero-child reconstruction", err)
		}
		if _, err := rebuild(current.Info, []expr.Expr{arg}); err == nil {
			t.Fatal("extra current-config operand")
		}
	}
	spec, err = current.Render()
	if err != nil || spec.Namespace != "pg_catalog" || spec.Name != "get_current_ts_config" || spec.Replay.Validate == nil || spec.Replay.Validate() != nil {
		t.Fatal("current configuration symbolic replay", err)
	}
	capture, ok, err = current.Capture(expr.CaptureFunction)
	if err != nil || !ok || capture.Check(current, 0) != nil {
		t.Fatal("zero-argument capture", err)
	}
	if _, err := current.Eval(c); err == nil {
		t.Fatal("invented configuration without statement authority")
	}
	bad := current
	bad.Usage = types[sqltype.Text].Usage()
	if bad.Validate() == nil {
		t.Fatal("current configuration accepted TEXT result")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Context = ctx
	for _, node := range []expr.Expr{n, current} {
		if value, err := node.Eval(c); value.Type() != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("provider canceled result publication", value, err)
		}
	}
	for _, name := range []string{"array_to_tsvector", "get_current_ts_config"} {
		definition, ok := Lookup(name)
		if !ok || definition.Bind == nil || !definition.ExactSpelling || !definition.SharedGates {
			t.Fatal("registry ownership", name)
		}
	}
}
