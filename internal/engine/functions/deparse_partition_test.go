package functions

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func TestDeparsePartitionProtocols(t *testing.T) {
	for _, tc := range []struct {
		signature DeparseSignature
		name      string
		oid       uint32
	}{
		{DeparsePartitionConstraint, "pg_get_partition_constraintdef", 3408},
		{DeparsePartitionKey, "pg_get_partkeydef", 3352},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if int(tc.signature) >= DeparseSignatureCount {
				t.Fatal("partition inquiry signature is not registered")
			}
			if tc.signature.Name() != tc.name || tc.signature.PrettyIndex() != -1 {
				t.Fatal("wrong released single-OID signature")
			}
			d := temporalTestTypes()
			arg := temporalTestLiteral(sqltype.Null(d[sqltype.Oid]))
			n := Deparse{Info: expr.Info{Usage: d[sqltype.Text].Usage(), Property: expr.Properties{Volatility: expr.Stable}, RoutineReference: jsonpathArrayUnitReference(tc.oid)}, Signature: tc.signature, Args: []expr.Expr{arg}}
			if err := n.Validate(); err != nil {
				t.Fatal(err)
			}
			if n.IntrinsicVolatility() != expr.Stable || n.PlanningFold() != expr.FoldNever || n.PreflightFold() != expr.PreflightChildren {
				t.Fatal("inquiry folded or lost STABLE declaration")
			}
			if nullable, known := n.ResultNullability(expr.ResultSelect); !nullable || !known {
				t.Fatal("TEXT nullability")
			}
			if a, b, c := n.SnapshotDemand(func(e expr.Expr) bool { return reflect.DeepEqual(e, arg) }); a || b || c {
				t.Fatal("strict NULL acquired catalog snapshot")
			}
			if a, b, c := n.SnapshotDemand(func(expr.Expr) bool { return false }); !a || b || c {
				t.Fatal("nonnull inquiry lost snapshot demand")
			}
			v, err := n.Eval(&temporalTestContext{Context: context.Background()})
			if err != nil || !v.IsNull() || v.Type() != d[sqltype.Text] {
				t.Fatal("strict typed NULL without inquiry runtime", v, err)
			}
			captured, admitted, err := n.Capture(expr.CaptureFunction)
			if err != nil || !admitted || captured.Check(n, 1) != nil || !reflect.DeepEqual(captured.Args, n.Args) {
				t.Fatal("written-operand capture", captured, err)
			}
			if _, admitted, err := n.Capture(expr.CaptureOperator); err != nil || admitted {
				t.Fatal("operator capture")
			}
			rendered, err := n.Render()
			if err != nil || rendered.Form != expr.FormCall || rendered.Name != tc.name || !rendered.Replay.CatalogSnapshot || len(rendered.Args) != 1 || !reflect.DeepEqual(rendered.Args[0].Expr, arg) {
				t.Fatal("stored-call rendering", rendered, err)
			}
			before := append([]expr.Expr(nil), n.Args...)
			rebuilt, err := n.Rebuild(n.Info, append([]expr.Expr(nil), n.Args...))
			if err != nil {
				t.Fatal(err)
			}
			copy := rebuilt.(Deparse)
			if copy.Signature != n.Signature || copy.Info != n.Info || copy.CallRoutineReference().RoutineOID() != tc.oid || !reflect.DeepEqual(n.Args, before) {
				t.Fatal("reconstruction lost provenance or mutated recipe")
			}
			if !n.SameGroup(copy, func(a, b expr.Expr) bool { return reflect.DeepEqual(a, b) }, true) {
				t.Fatal("reconstruction identity")
			}
			if _, err := n.Rebuild(n.Info, nil); err == nil {
				t.Fatal("wrong reconstruction arity accepted")
			}
			for _, args := range [][]expr.Expr{nil, {arg, arg}, {temporalTestLiteral(sqltype.Null(d[sqltype.Text]))}} {
				bad := n
				bad.Args = args
				if err := bad.Validate(); err == nil {
					t.Fatal("invalid formal accepted")
				}
			}
			failure := errors.New("operand failure")
			failing := arg
			failing.err = failure
			copy.Args = []expr.Expr{failing}
			if _, err := copy.Eval(&temporalTestContext{Context: context.Background()}); !errors.Is(err, failure) {
				t.Fatal("strictness swallowed child failure", err)
			}
			volatile := arg
			volatile.Property.Volatility = expr.Volatile
			copy.Args = []expr.Expr{volatile}
			if err := copy.Validate(); err == nil {
				t.Fatal("lost volatile operand properties accepted")
			}
			copy.Property = copy.GroupProperties(copy.Args)
			if copy.Properties().Volatility != expr.Volatile || copy.Validate() != nil {
				t.Fatal("volatile operand not merged")
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := n.Eval(&temporalTestContext{Context: ctx}); !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation replaced with NULL", err)
			}
		})
	}
}
