package functions

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func TestFTSHeadlineProviderLifecycle(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		for _, options := range []bool{false, true} {
			op := sqltype.FTSHeadlineFunction{Explicit: explicit, Options: options}
			types := inspectCarrierTypes()
			var args [4]expr.Expr
			var order []int
			for i := range op.Arity() {
				id := op.Formal(i)
				var value sqltype.Value
				var err error
				if id == sqltype.Regconfig {
					value, err = sqltype.RegTSValue(types[id], 13164)
				} else {
					input := "Cat"
					if id == sqltype.TSQuery {
						input = "cat"
					}
					if options && i == op.Arity()-1 {
						input = "HighlightAll=true"
					}
					value, err = types[id].Input(t.Context(), input, -1)
				}
				if err != nil {
					t.Fatal(err)
				}
				args[i] = stringUnitArgument{Info: expr.Info{Usage: types[id].Usage()}, value: value, order: &order, index: i}
			}
			node, err := NewFTSHeadline(expr.Info{Usage: types[sqltype.Text].Usage()}, op, args)
			if err != nil || node.ValidateStoredProvider() != nil {
				t.Fatal(op, err)
			}
			volatility := expr.Stable
			if explicit {
				volatility = expr.Immutable
			}
			if node.IntrinsicVolatility() != volatility || node.PreflightFold() != expr.PreflightNullOrEvaluate {
				t.Fatal("headline fold policy")
			}
			render, err := node.Render()
			if err != nil || render.Namespace != "pg_catalog" || render.Replay.Validate == nil || render.Replay.Validate() != nil {
				t.Fatal("headline render", err)
			}
			capture, ok, err := node.Capture(expr.CaptureFunction)
			if err != nil || !ok || capture.Check(node, op.Arity()) != nil {
				t.Fatal("headline capture", err)
			}
			for i := range op.Arity() {
				operand, err := node.CaptureOperand(i)
				if err != nil || operand != args[i] {
					t.Fatal("headline source order", i, err)
				}
			}
			if _, err := node.CaptureOperand(op.Arity()); err == nil {
				t.Fatal("capture overflow")
			}
			for _, change := range []func(expr.Info, []expr.Expr) (expr.Expr, error){node.Rebuild, node.Substitute} {
				changed, err := change(node.Info, node.Children())
				if err != nil || changed.(FTSHeadline).Validate() != nil {
					t.Fatal("headline rebuild", err)
				}
				if _, err := change(node.Info, nil); err == nil {
					t.Fatal("missing children")
				}
			}
			if !node.SameGroup(node, func(a, b expr.Expr) bool { return a == b }, false) {
				t.Fatal("headline group identity")
			}
			if _, err := node.Eval(&stringUnitContext{Context: t.Context()}); err == nil || !strings.Contains(err.Error(), "lacks statement authority") {
				t.Fatal("invented headline runtime", err)
			}
			want := make([]int, op.Arity())
			for i := range want {
				want[i] = i
			}
			if !reflect.DeepEqual(order, want) {
				t.Fatal("headline evaluation order", order, want)
			}
			left := args[0].(stringUnitArgument)
			left.value, left.literal = sqltype.Null(left.Usage.Type), true
			node.Args[0] = left
			order = nil
			value, err := node.Eval(&stringUnitContext{Context: t.Context()})
			if err != nil || !value.IsNull() || value.Type() != node.Usage.Type || len(order) != 0 {
				t.Fatal("headline literal NULL preflight", err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := node.Eval(&stringUnitContext{Context: ctx}); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		}
	}
	definition, ok := Lookup("ts_headline")
	if !ok || definition.Bind == nil || !definition.ExactSpelling || !definition.SharedGates {
		t.Fatal("headline registration")
	}
}
