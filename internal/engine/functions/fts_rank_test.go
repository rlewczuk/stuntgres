package functions

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func TestFTSRankProviderLifecycle(t *testing.T) {
	for _, density := range []bool{false, true} {
		for _, weighted := range []bool{false, true} {
			for _, normalized := range []bool{false, true} {
				op := sqltype.FTSRankFunction{Density: density, Weighted: weighted, Normalized: normalized}
				types := inspectCarrierTypes()
				var args [4]expr.Expr
				var order []int
				for i := range op.Arity() {
					id := op.Formal(i)
					input := "cat:1"
					switch id {
					case sqltype.TSQuery:
						input = "cat"
					case sqltype.Int4:
						input = "0"
					case 1021:
						input = "{0.1,0.2,0.4,1}"
					}
					value, err := types[id].Input(t.Context(), input, -1)
					if err != nil {
						t.Fatal(err)
					}
					args[i] = stringUnitArgument{Info: expr.Info{Usage: types[id].Usage()}, value: value, order: &order, index: i}
				}
				node, err := NewFTSRank(expr.Info{Usage: types[sqltype.Float4].Usage()}, op, args)
				if err != nil || node.ValidateStoredProvider() != nil {
					t.Fatal(op, err)
				}
				if node.IntrinsicVolatility() != expr.Immutable || node.PreflightFold() != expr.PreflightNullOrEvaluate {
					t.Fatal("rank fold policy")
				}
				render, err := node.Render()
				if err != nil || render.Namespace != "pg_catalog" || render.Replay.Validate == nil || render.Replay.Validate() != nil {
					t.Fatal("rank render", err)
				}
				capture, ok, err := node.Capture(expr.CaptureFunction)
				if err != nil || !ok || capture.Check(node, op.Arity()) != nil {
					t.Fatal("rank capture", err)
				}
				for i := range op.Arity() {
					operand, err := node.CaptureOperand(i)
					if err != nil || operand != args[i] {
						t.Fatal("rank source order", i, err)
					}
				}
				if _, err := node.CaptureOperand(op.Arity()); err == nil {
					t.Fatal("capture overflow")
				}
				for _, change := range []func(expr.Info, []expr.Expr) (expr.Expr, error){node.Rebuild, node.Substitute} {
					changed, err := change(node.Info, node.Children())
					if err != nil || changed.(FTSRank).Validate() != nil {
						t.Fatal("rank rebuild", err)
					}
					if _, err := change(node.Info, nil); err == nil {
						t.Fatal("missing children")
					}
				}
				if !node.SameGroup(node, func(a, b expr.Expr) bool { return a == b }, false) {
					t.Fatal("rank group identity")
				}
				if _, err := node.Eval(&stringUnitContext{Context: t.Context()}); err != nil {
					t.Fatal(err)
				}
				want := make([]int, op.Arity())
				for i := range want {
					want[i] = i
				}
				if !reflect.DeepEqual(order, want) {
					t.Fatal("evaluation order", order, want)
				}
				bad := args
				bad[0] = nil
				if _, err := NewFTSRank(node.Info, op, bad); err == nil {
					t.Fatal("missing argument")
				}
				left := args[0].(stringUnitArgument)
				left.value, left.literal = sqltype.Null(left.Usage.Type), true
				node.Args[0] = left
				order = nil
				value, err := node.Eval(&stringUnitContext{Context: t.Context()})
				if err != nil || !value.IsNull() || value.Type() != node.Usage.Type || len(order) != 0 {
					t.Fatal("rank literal NULL preflight", err)
				}
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				if _, err := node.Eval(&stringUnitContext{Context: ctx}); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			}
		}
	}
	for _, name := range []string{"ts_rank", "ts_rank_cd"} {
		definition, ok := Lookup(name)
		if !ok || definition.Bind == nil || !definition.ExactSpelling || !definition.SharedGates {
			t.Fatal("rank registration", name)
		}
	}
}
