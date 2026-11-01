package functions

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func TestDeparseStatisticsProtocols(t *testing.T) {
	if DeparseStatistics.Name() != "pg_get_statisticsobjdef" || DeparseStatistics.PrettyIndex() != -1 {
		t.Fatal("wrong single-OID signature")
	}
	formal := deparseSignatures[DeparseStatistics].Args
	if !reflect.DeepEqual(formal, []sqltype.ID{sqltype.Oid}) {
		t.Fatal("wrong formal", formal)
	}
	count := 0
	for _, signature := range deparseSignatures {
		if signature.name == "pg_get_statisticsobjdef" {
			count++
		}
	}
	if count != 1 {
		t.Fatal("invented overload", count)
	}
	d := temporalTestTypes()
	arg := temporalTestLiteral(sqltype.Null(d[sqltype.Oid]))
	n := Deparse{Info: expr.Info{Usage: d[sqltype.Text].Usage(), Property: expr.Properties{Volatility: expr.Stable}, RoutineReference: jsonpathArrayUnitReference(3415)}, Signature: DeparseStatistics, Args: []expr.Expr{arg}}
	if err := n.Validate(); err != nil {
		t.Fatal(err)
	}
	if n.CallRoutineReference().RoutineOID() != 3415 || n.IntrinsicVolatility() != expr.Stable || n.PlanningFold() != expr.FoldNever || n.PreflightFold() != expr.PreflightChildren {
		t.Fatal("routine provenance or planning protocol")
	}
	if nullable, known := n.ResultNullability(expr.ResultSelect); !nullable || !known {
		t.Fatal("nullable TEXT")
	}
	if a, b, c := n.SnapshotDemand(func(e expr.Expr) bool { return reflect.DeepEqual(e, arg) }); a || b || c {
		t.Fatal("strict NULL demanded authority")
	}
	if a, b, c := n.SnapshotDemand(func(expr.Expr) bool { return false }); !a || b || c {
		t.Fatal("nonnull demand")
	}
	v, err := n.Eval(&temporalTestContext{Context: context.Background()})
	if err != nil || !v.IsNull() || v.Type() != d[sqltype.Text] {
		t.Fatal("strict TEXT NULL", v, err)
	}
	capture, admitted, err := n.Capture(expr.CaptureFunction)
	if err != nil || !admitted || capture.Check(n, 1) != nil || !reflect.DeepEqual(capture.Args, n.Args) {
		t.Fatal("written operand capture", capture, err)
	}
	if _, admitted, err := n.Capture(expr.CaptureOperator); err != nil || admitted {
		t.Fatal("operator capture")
	}
	rendered, err := n.Render()
	if err != nil || rendered.Form != expr.FormCall || rendered.Name != "pg_get_statisticsobjdef" || !rendered.Replay.CatalogSnapshot || len(rendered.Args) != 1 || !reflect.DeepEqual(rendered.Args[0].Expr, arg) {
		t.Fatal("stored-call rendering", rendered, err)
	}
	before := append([]expr.Expr(nil), n.Args...)
	rebuilt, err := n.Rebuild(n.Info, append([]expr.Expr(nil), n.Args...))
	if err != nil {
		t.Fatal(err)
	}
	copy := rebuilt.(Deparse)
	if copy.Info != n.Info || copy.Signature != n.Signature || !reflect.DeepEqual(n.Args, before) || !n.SameGroup(copy, func(a, b expr.Expr) bool { return reflect.DeepEqual(a, b) }, true) {
		t.Fatal("rebuild lost immutable provenance")
	}
	for _, args := range [][]expr.Expr{nil, {arg, arg}, {arg, temporalTestLiteral(sqltype.Boolean(d[sqltype.Bool], true))}, {temporalTestLiteral(sqltype.Null(d[sqltype.Text]))}, {nil}} {
		bad := n
		bad.Args = args
		if bad.Validate() == nil {
			t.Fatal("malformed formal or pretty overload accepted")
		}
	}
	if _, err := n.Rebuild(n.Info, nil); err == nil {
		t.Fatal("malformed reconstruction")
	}
	bad := n
	bad.Usage = d[sqltype.Int4].Usage()
	if bad.Validate() == nil {
		t.Fatal("wrong result")
	}
	failure := errors.New("statistics operand failure")
	failing := arg
	failing.err = failure
	copy.Args = []expr.Expr{failing}
	if _, err := copy.Eval(&temporalTestContext{Context: context.Background()}); !errors.Is(err, failure) {
		t.Fatal("child failure swallowed", err)
	}
	volatile := arg
	volatile.Property.Volatility = expr.Volatile
	copy.Args = []expr.Expr{volatile}
	if copy.Validate() == nil {
		t.Fatal("incorrect properties admitted")
	}
	copy.Property = copy.GroupProperties(copy.Args)
	if copy.Validate() != nil || copy.Properties().Volatility != expr.Volatile {
		t.Fatal("child properties not merged")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := n.Eval(&temporalTestContext{Context: ctx}); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation hidden by strictness", err)
	}
}
