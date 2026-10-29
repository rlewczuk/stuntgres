package functions

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func TestFTSMatchProviderLifecycle(t *testing.T) {
	for op := sqltype.FTSMatchVQ; op <= sqltype.FTSMatchTQ; op++ {
		for _, function := range []bool{false, true} {
			n, c, order := ftsProviderUnit(t, op, function)
			fresh, _, _ := ftsProviderUnit(t, op, function)
			wantVol := expr.Immutable
			text := op == sqltype.FTSMatchTT || op == sqltype.FTSMatchTQ
			if text {
				wantVol = expr.Stable
			}
			if n.ValidateStoredProvider() != nil || n.IntrinsicVolatility() != wantVol || n.PreflightFold() != expr.PreflightNullOrEvaluate || n.LooksLikeFunction() != function {
				t.Fatal("provider contract", op)
			}
			_, err := n.Eval(c)
			if text && (err == nil || err.Error() != "stuntgres: text search conversion lacks statement authority") {
				t.Fatal("runtime authority", op, err)
			}
			if !text && err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(*order, []int{0, 1}) {
				t.Fatal("written order", *order)
			}
			for _, rebuild := range []func(expr.Info, []expr.Expr) (expr.Expr, error){n.Rebuild, n.Substitute} {
				if _, err := rebuild(n.Info, fresh.Children()); err == nil {
					t.Fatal("foreign children admitted", op)
				}
				got, err := rebuild(fresh.Info, fresh.Children())
				if err != nil || got.(FTS).Validate() != nil || got.(FTS).Plan == n.Plan {
					t.Fatal("rebuild authority", op, err)
				}
				if _, err := rebuild(n.Info, nil); err == nil {
					t.Fatal("missing children")
				}
			}
			spec, err := n.Render()
			if err != nil || spec.Namespace != "pg_catalog" || spec.Replay.Validate == nil || spec.Replay.Validate() != nil {
				t.Fatal(spec, err)
			}
			form := expr.FormInfix
			kind := expr.CaptureOperator
			if function {
				form = expr.FormCall
				kind = expr.CaptureFunction
			}
			if spec.Form != form {
				t.Fatal(spec.Form)
			}
			capture, ok, err := n.Capture(kind)
			if err != nil || !ok || capture.Check(n, 2) != nil {
				t.Fatal(capture, err)
			}
			for i := range 2 {
				arg, err := capture.At(n, i)
				if err != nil || arg != n.Args[i] {
					t.Fatal("capture order", i, err)
				}
			}
			if _, err := n.CaptureOperand(2); err == nil {
				t.Fatal("capture range")
			}
			same := func(a, b expr.Expr) bool { return a.ResultUsage() == b.ResultUsage() }
			if !n.SameGroup(n, same, false) || n.SameGroup(fresh, same, false) {
				t.Fatal("group authority")
			}
			changed := n
			changed.Namespace = "other"
			if n.SameGroup(changed, same, false) {
				t.Fatal("namespace identity")
			}
			changed = n
			changed.Function = !function
			if n.SameGroup(changed, same, false) {
				t.Fatal("routine identity")
			}
			bad := n
			bad.Args[0] = nil
			if bad.Validate() == nil {
				t.Fatal("invalid provider")
			}
			left := n.Args[0].(stringUnitArgument)
			left.value = sqltype.Null(left.Usage.Type)
			n.Args[0] = left
			*order = nil
			value, err := n.Eval(c)
			if err != nil || !value.IsNull() || value.Type() != n.Usage.Type || !reflect.DeepEqual(*order, []int{0, 1}) {
				t.Fatal("runtime strict NULL", value, err, *order)
			}
			right := n.Args[1].(stringUnitArgument)
			failure := errors.New("match child failure")
			right.err = failure
			n.Args[1] = right
			if _, err := n.Eval(c); !errors.Is(err, failure) {
				t.Fatal("NULL hid child error", err)
			}
			left.literal = true
			n.Args[0] = left
			*order = nil
			value, err = n.Eval(c)
			if err != nil || !value.IsNull() || len(*order) != 0 {
				t.Fatal("literal NULL preflight", value, err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			c.Context = ctx
			if _, err := n.Eval(c); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		}
	}
	for _, name := range []string{"ts_match_vq", "ts_match_qv", "ts_match_tt", "ts_match_tq"} {
		d, ok := Lookup(name)
		if !ok || d.Bind == nil || !d.SharedGates || !d.ExactSpelling {
			t.Fatal(name)
		}
	}
}
