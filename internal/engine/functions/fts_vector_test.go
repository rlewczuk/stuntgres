package functions

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func TestFTSVectorFunctionsProviderLifecycle(t *testing.T) {
	if FTSVectorRoutineProvider != 1<<32 || FTSParserRoutineProvider != 1<<31 || FTSVectorRoutineProvider&FTSParserRoutineProvider != 0 {
		t.Fatal("provider authority aliases or existing identities changed")
	}
	for _, op := range []sqltype.FTSVectorFunction{sqltype.FTSVectorLength, sqltype.FTSSetWeight, sqltype.FTSSetSelectedWeight, sqltype.FTSStrip, sqltype.FTSDelete, sqltype.FTSDeleteArray, sqltype.FTSFilter, sqltype.FTSVectorToArray} {
		types := inspectCarrierTypes()
		var args [3]expr.Expr
		order := []int{}
		for i := range op.Arity() {
            input := "a:1A b:2"
            switch op.Formal(i) {case sqltype.Char: input="A";case sqltype.Text: input="a";case 1009: input="{a}";case 1002: input="{A}"}
			v, err := types[op.Formal(i)].Input(context.Background(), input, -1)
			if err != nil {
				t.Fatal(err)
			}
			args[i] = stringUnitArgument{Info: expr.Info{Usage: types[op.Formal(i)].Usage()}, value: v, order: &order, index: i}
		}
		node, err := NewFTSVector(expr.Info{Usage: types[op.ResultID()].Usage()}, op, args)
		if err != nil {
			t.Fatal(err)
		}
		if err := node.ValidateStoredProvider(); err != nil {
			t.Fatal(err)
		}
		volatility, preflight := expr.Immutable, expr.PreflightNullOrEvaluate

		if node.IntrinsicVolatility() != volatility || node.PreflightFold() != preflight {
			t.Fatal("volatility/fold policy", op)
		}
		spec, err := node.Render()
		if err != nil || spec.Namespace != "pg_catalog" || spec.Replay.Validate == nil || spec.Replay.Validate() != nil {
			t.Fatal("render", err)
		}
		capture, ok, err := node.Capture(expr.CaptureFunction)
		if err != nil || !ok || capture.Check(node, op.Arity()) != nil {
			t.Fatal("capture", err)
		}
		for i := range op.Arity() {
			operand, err := node.CaptureOperand(i)
			if err != nil || operand != args[i] {
				t.Fatal("operand", err)
			}
		}
		if _, err := node.CaptureOperand(op.Arity()); err == nil {
			t.Fatal("capture overflow")
		}
		rebuilt, err := node.Rebuild(node.Info, node.Children())
		if err != nil || rebuilt.(FTSVector).Validate() != nil {
			t.Fatal("rebuild", err)
		}
		changed, err := node.Substitute(node.Info, node.Children())
		if err != nil || changed.(FTSVector).Validate() != nil {
			t.Fatal("substitute", err)
		}
		if _, err := node.Rebuild(node.Info, nil); err == nil {
			t.Fatal("missing child accepted")
		}
		same := func(a, b expr.Expr) bool { return a == b }
		if !node.SameGroup(node, same, false) {
			t.Fatal("group identity")
		}
		c := &stringUnitContext{Context: context.Background()}
		_, err = node.Eval(c)
        if err != nil { t.Fatal(err) }
		left := args[0].(stringUnitArgument)
		left.value, left.literal = sqltype.Null(left.Usage.Type), true
		node.Args[0] = left
		order = nil
		v, err := node.Eval(c)
		if err != nil || !v.IsNull() || v.Type() != node.Usage.Type || len(order) != 0 {
			t.Fatal("literal strict NULL", err)
		}
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		c.Context = canceled
		if _, err := node.Eval(c); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"setweight", "strip", "ts_delete", "ts_filter", "tsvector_to_array"} {
		d, ok := Lookup(name)
		if !ok || d.Bind == nil || !d.ExactSpelling || !d.SharedGates {
			t.Fatal("registry", name)
		}
	}
}
