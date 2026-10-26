package functions

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func ftsProviderUnit(t *testing.T, op sqltype.FTSOperator, function bool) (FTS, *stringUnitContext, *[]int) {
	t.Helper()
	types := inspectCarrierTypes()
	order := []int{}
	var args [3]expr.Expr
	for i := range op.Arity() {
		typ := types[op.Formal(i)]
		input := "a"
		if i == 1 {
			input = "b"
		}
		if i == 2 {
			input = "2"
		}
		v, err := typ.Input(context.Background(), input, -1)
		if err != nil {
			t.Fatal(err)
		}
		args[i] = stringUnitArgument{Info: expr.Info{Usage: typ.Usage()}, value: v, order: &order, index: i}
	}
	node, err := NewFTS(expr.Info{Usage: types[op.ResultID()].Usage()}, op, args, "pg_catalog", function)
	if err != nil {
		t.Fatal(err)
	}
	return node, &stringUnitContext{Context: context.Background()}, &order
}
func TestFTSOpsProviderLifecycle(t *testing.T) {
	for op := sqltype.FTSAnd; op <= sqltype.FTSConcat; op++ {
		for _, function := range []bool{false, true} {
			if op == sqltype.FTSPhraseDistance && !function {
				continue
			}
			n, c, order := ftsProviderUnit(t, op, function)
			fresh, _, _ := ftsProviderUnit(t, op, function)
			if n.ValidateStoredProvider() != nil || n.IntrinsicVolatility() != expr.Immutable || n.PreflightFold() != expr.PreflightNullOrEvaluate || n.LooksLikeFunction() != function {
				t.Fatal("provider contracts", op)
			}
			if _, err := n.Eval(c); err != nil {
				t.Fatal(err)
			}
			wantOrder := []int{0}
			if op.Arity() > 1 {
				wantOrder = append(wantOrder, 1)
			}
			if op.Arity() > 2 {
				wantOrder = append(wantOrder, 2)
			}
			if !reflect.DeepEqual(*order, wantOrder) {
				t.Fatal("evaluation order", op, *order)
			}
			rebuilt, err := n.Rebuild(fresh.Info, fresh.Children())
			if err != nil || rebuilt.(FTS).Validate() != nil || rebuilt.(FTS).Plan == n.Plan {
				t.Fatal("rebuild ownership", op, err)
			}
			if _, err := n.Rebuild(n.Info, fresh.Children()); err == nil {
				t.Fatal("foreign result admitted", op)
			}
			if _, err := n.Rebuild(n.Info, nil); err == nil {
				t.Fatal("missing operands", op)
			}
			bad := n
			bad.Args[0] = nil
			if bad.Validate() == nil {
				t.Fatal("missing stored operand", op)
			}
			spec, err := n.Render()
			if err != nil || spec.Namespace != "pg_catalog" || spec.Replay.Validate == nil || spec.Replay.Validate() != nil {
				t.Fatal("render contract", op, spec, err)
			}
			wantForm := expr.FormInfix
			if function {
				wantForm = expr.FormCall
			} else if op == sqltype.FTSNot {
				wantForm = expr.FormTemplate
				if len(spec.Pieces) != 3 || len(spec.Replay.Pieces) != 5 {
					t.Fatal("prefix replay", spec)
				}
			}
			if spec.Form != wantForm {
				t.Fatal("SQL form", op, spec.Form)
			}
			kind := expr.CaptureOperator
			if function {
				kind = expr.CaptureFunction
			}
			captured, ok, err := n.Capture(kind)
			if err != nil || !ok || !captured.Indexed || captured.Check(n, op.Arity()) != nil {
				t.Fatal("capture", op, captured, err)
			}
			for i := range op.Arity() {
				arg, err := captured.At(n, i)
				if err != nil || arg != n.Args[i] {
					t.Fatal("capture order", op, i, err)
				}
			}
			if _, err := n.CaptureOperand(op.Arity()); err == nil {
				t.Fatal("capture range", op)
			}
			same := func(a, b expr.Expr) bool { return a.ResultUsage() == b.ResultUsage() }
			if !n.SameGroup(n, same, false) || n.SameGroup(fresh, same, false) {
				t.Fatal("group authority", op)
			}
			different := n
			different.Function = !function
			if n.SameGroup(different, same, false) {
				t.Fatal("syntax identity", op)
			}
			changed, err := n.Substitute(n.Info, n.Children())
			if err != nil || changed.(FTS).Validate() != nil {
				t.Fatal("substitute", err)
			}
			left := n.Args[0].(stringUnitArgument)
			left.value = sqltype.Null(left.Usage.Type)
			n.Args[0] = left
			if op.Arity() > 1 {
				right := n.Args[1].(stringUnitArgument)
				failure := errors.New("FTS child failure")
				right.err = failure
				n.Args[1] = right
				if _, err := n.Eval(c); !errors.Is(err, failure) {
					t.Fatal("runtime NULL hid child error", err)
				}
			}
			left.literal = true
			n.Args[0] = left
			*order = nil
			value, err := n.Eval(c)
			if err != nil || !value.IsNull() || value.Type() != n.Usage.Type || len(*order) != 0 {
				t.Fatal("literal NULL", op, value, err)
			}
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			c.Context = canceled
			if _, err := n.Eval(c); !errors.Is(err, context.Canceled) {
				t.Fatal("entry cancellation", err)
			}
		}
	}
	for _, name := range []string{"tsvector_concat", "tsquery_and", "tsquery_or", "tsquery_not", "tsquery_phrase", "tsq_mcontains", "tsq_mcontained"} {
		definition, ok := Lookup(name)
		if !ok || definition.Bind == nil || !definition.ExactSpelling || !definition.SharedGates {
			t.Fatal("genuine routine binder", name)
		}
	}
	if definition, ok := Lookup("to_tsvector"); !ok || definition.Bind == nil || !definition.SharedGates {
		t.Fatal("explicit configuration conversion binder")
	}
	for _, name := range []string{"to_tsquery", "tsquery_cmp", "tsvector_cmp", "ts_match_vq"} {
		if _, ok := Lookup(name); ok {
			t.Fatal("adjacent routine claimed", name)
		}
	}
}
