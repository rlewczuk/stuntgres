package functions

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/fts"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestFTSJSONLifecycle(t *testing.T) {
	types := inspectCarrierTypes()
	for _, input := range []sqltype.ID{sqltype.JSON, sqltype.JSONB} {
		for _, filtered := range []bool{false, true} {
			for _, explicit := range []bool{false, true} {
				var order []int
				node := FTSConversion{Info: expr.Info{Usage: types[sqltype.TSVector].Usage()}, Kind: fts.VectorConversion, Input: input, Filtered: filtered, Explicit: explicit}
				for i := range node.arity() {
					typ := types[input]
					if filtered && i == node.arity()-1 {
						typ = types[sqltype.JSONB]
					}
					if explicit && i == 0 {
						typ = types[sqltype.Regconfig]
					}
					node.Args[i] = stringUnitArgument{Info: expr.Info{Usage: typ.Usage()}, value: sqltype.Null(typ), order: &order, index: i}
				}
				volatility := expr.Stable
				if explicit {
					volatility = expr.Immutable
				}
				if err := node.Validate(); err != nil || node.IntrinsicVolatility() != volatility {
					t.Fatal(node, err)
				}
				c := &stringUnitContext{Context: context.Background()}
				value, err := node.Eval(c)
				if err != nil || !value.IsNull() || value.Type() != types[sqltype.TSVector] || len(order) != node.arity() {
					t.Fatal(value, order, err)
				}
				for _, rebuild := range []func(expr.Info, []expr.Expr) (expr.Expr, error){node.Rebuild, node.Substitute} {
					rebuilt, err := rebuild(node.Info, node.Children())
					if err != nil || !node.SameGroup(rebuilt, func(a, b expr.Expr) bool { return reflect.DeepEqual(a, b) }, true) {
						t.Fatal(rebuilt, err)
					}
				}
				if err := node.ValidateStoredProvider(); err != nil {
					t.Fatal(err)
				}
				spec, err := node.Render()
				if err != nil || spec.Namespace != "pg_catalog" {
					t.Fatal(spec, err)
				}
				capture, ok, err := node.Capture(expr.CaptureFunction)
				if err != nil || !ok || capture.Count != node.arity() {
					t.Fatal(capture, ok, err)
				}
				for i := range node.arity() {
					if got, err := node.CaptureOperand(i); err != nil || got != node.Args[i] {
						t.Fatal(got, err)
					}
				}
				if _, err := node.CaptureOperand(node.arity()); err == nil {
					t.Fatal("out of range capture")
				}
				other := node
				other.Input = sqltype.Text
				if node.SameGroup(other, func(a, b expr.Expr) bool { return true }, false) {
					t.Fatal("input kinds grouped")
				}
				other = node
				other.Filtered = !filtered
				if node.SameGroup(other, func(a, b expr.Expr) bool { return true }, false) {
					t.Fatal("filtered layouts grouped")
				}
				bad := node
				bad.Args[node.arity()-1] = nil
				if bad.Validate() == nil {
					t.Fatal("missing operand")
				}
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				c.Context = ctx
				if _, err := node.Eval(c); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				c.Context = context.Background()
				first := node.Args[0].(stringUnitArgument)
				first.literal = true
				node.Args[0] = first
				order = nil
				if v, err := node.Eval(c); err != nil || !v.IsNull() || len(order) != 0 {
					t.Fatal(v, order, err)
				}
				if node.arity() > 1 {
					first.literal = false
					node.Args[0] = first
					last := node.Args[node.arity()-1].(stringUnitArgument)
					failure := errors.New("child failure")
					last.err = failure
					node.Args[node.arity()-1] = last
					if _, err := node.Eval(c); err != failure {
						t.Fatal("strictness hid child producer", err)
					}
				}
			}
		}
	}
}
