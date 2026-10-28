package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func legacySnapshotProtocols(t *testing.T, node expr.Expr, name string, oid uint32, result sqltype.ID, volatility expr.Volatility, parallel byte, set bool, formal ...sqltype.ID) {
	t.Helper()
	declaration, err := exec.ExpressionRoutineDescriptor(node)
	if err != nil || declaration == nil || declaration.Builtin == nil || uint32(declaration.Address().ObjectID) != oid || !declaration.Strict || declaration.Volatility != volatility || declaration.Builtin.Parallel != parallel || declaration.ReturnsSet() != set || node.ResultUsage().Type.ID() != result {
		t.Fatal("released declaration", name, declaration, err)
	}
	if err := node.(expr.StoredProvider).ValidateStoredProvider(); err != nil {
		t.Fatal(name, err)
	}
	composite := node.(expr.Composite)
	children := composite.Children()
	if len(children) != len(formal) {
		t.Fatal("formal count", name, children)
	}
	for i, child := range children {
		if child.ResultUsage().Type.BaseType().ID() != formal[i] {
			t.Fatal("formal identity", name, i, child.ResultUsage())
		}
	}
	capture := node.(expr.Capturable)
	operands, ok, err := capture.Capture(expr.CaptureFunction)
	if err != nil || !ok || operands.Count != len(children) {
		t.Fatal("function capture", operands, ok, err)
	}
	for i, child := range children {
		got, err := operands.At(capture, i)
		if err != nil || !reflect.DeepEqual(got, child) {
			t.Fatal("captured operand", i, err)
		}
	}
	if _, ok, err := capture.Capture(expr.CaptureOperator); err != nil || ok {
		t.Fatal("nonfunction capture", ok, err)
	}
	info := expr.InfoOf(node)
	info.Occurrence++
	for _, rebuild := range []func(expr.Info, []expr.Expr) (expr.Expr, error){composite.Rebuild, node.(expr.Substitutable).Substitute} {
		next, err := rebuild(info, children)
		if err != nil || expr.InfoOf(next) != info || next.ResultUsage().Type != node.ResultUsage().Type {
			t.Fatal("reconstruction ownership", next, err)
		}
		if ref := expr.InfoOf(next).CallRoutineReference(); ref != expr.InfoOf(node).CallRoutineReference() {
			t.Fatal("routine authority replaced")
		}
		if !node.(expr.GroupIdentity).SameGroup(next, func(a, b expr.Expr) bool { return reflect.DeepEqual(a, b) }, true) {
			t.Fatal("flavor changed on reconstruction")
		}
		rendering, err := next.(expr.Renderable).Render()
		if err != nil || rendering.Name != name || len(rendering.Args) != len(formal) {
			t.Fatal("legacy symbolic render", rendering, err)
		}
	}
	bad := expr.InfoOf(node)
	bad.Usage.Type = nil
	if _, err := composite.Rebuild(bad, children); err == nil {
		t.Fatal("missing result descriptor admitted")
	}
	admitted, returnsSet := expr.SourceTraits(node)
	if !admitted || returnsSet != set {
		t.Fatal("source cardinality", admitted, returnsSet)
	}
	if exec.PlannedVolatility(node) != volatility {
		t.Fatal("volatility", name)
	}
}

func TestLegacySnapshotBinding(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	for _, tc := range []struct {
		name, call string
		oid        uint32
		result     sqltype.ID
		volatility expr.Volatility
		set        bool
		formal     []sqltype.ID
	}{
		{"txid_current_snapshot", "txid_current_snapshot()", 2944, sqltype.TxidSnapshot, expr.Stable, false, nil},
		{"txid_snapshot_xmin", "txid_snapshot_xmin($1)", 2945, sqltype.Int8, expr.Immutable, false, []sqltype.ID{sqltype.TxidSnapshot}},
		{"txid_snapshot_xmax", "txid_snapshot_xmax($1)", 2946, sqltype.Int8, expr.Immutable, false, []sqltype.ID{sqltype.TxidSnapshot}},
		{"txid_snapshot_xip", "txid_snapshot_xip($1)", 2947, sqltype.Int8, expr.Immutable, true, []sqltype.ID{sqltype.TxidSnapshot}},
		{"txid_visible_in_snapshot", "txid_visible_in_snapshot($1,$2)", 2948, sqltype.Bool, expr.Immutable, false, []sqltype.ID{sqltype.Int8, sqltype.TxidSnapshot}},
	} {
		node, _, _ := strictNullUnitBind(t, e, op, scope, tc.call)
		legacySnapshotProtocols(t, node, tc.name, tc.oid, tc.result, tc.volatility, 's', tc.set, tc.formal...)
		wantFold := expr.FoldConstant
		if tc.set || tc.volatility == expr.Stable {
			wantFold = expr.FoldNever
		}
		if node.(expr.Folding).PlanningFold() != wantFold {
			t.Fatal("fold policy", tc.name)
		}
	}
	legacy, _, _ := strictNullUnitBind(t, e, op, scope, "txid_visible_in_snapshot($1,$2)")
	modern, _, _ := strictNullUnitBind(t, e, op, scope, "pg_visible_in_snapshot($1,$2)")
	if legacy.(expr.GroupIdentity).SameGroup(modern, func(expr.Expr, expr.Expr) bool { return true }, true) {
		t.Fatal("legacy/modern visibility flavors grouped despite distinct formals")
	}
	for _, cast := range []string{"smallint", "integer", "bigint", "strict_null_checked"} {
		node, _, _ := strictNullUnitBind(t, e, op, scope, "txid_visible_in_snapshot($1::"+cast+",$2)")
		if node.(expr.Composite).Children()[0].ResultUsage().Type.ID() != sqltype.Int8 {
			t.Fatal("ordinary widening not retained", cast)
		}
	}
}

func TestLegacySnapshotSource(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	node, b, evaluation := strictNullUnitBind(t, e, op, scope, "txid_snapshot_xip($1)")
	d := b.Params.Parameters[1].Type
	input, err := d.Input(context.Background(), "9223372036854775807:9223372036854775811:9223372036854775807,9223372036854775808,9223372036854775810", -1)
	if err != nil {
		t.Fatal(err)
	}
	evaluation.Parameters = []sqltype.Value{input}
	open := node.(expr.SourceOpening)
	a, err := open.OpenSource(&evaluation)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	z, err := open.OpenSource(&evaluation)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	payload, _ := input.Snapshot()
	if retained, err := a.Value.Snapshot(); err != nil || retained != payload || a.Values != nil || a.State != nil {
		t.Fatal("cursor copied or recaptured snapshot", err)
	}
	for _, cursor := range []*expr.SourceCursor{&a, &z} {
		v, ok, err := cursor.Next(context.Background())
		if err != nil || !ok {
			t.Fatal(ok, err)
		}
		if n, err := v.Integer(); err != nil || n != 9223372036854775807 {
			t.Fatal(n, err)
		}
	}
	a.Close()
	for _, want := range []int64{-9223372036854775808, -9223372036854775806} {
		v, ok, err := z.Next(context.Background())
		if err != nil || !ok {
			t.Fatal(ok, err)
		}
		if n, err := v.Integer(); err != nil || n != want {
			t.Fatal(n, want, err)
		}
	}
	if _, ok, err := z.Next(context.Background()); err != nil || ok {
		t.Fatal("EOF", ok, err)
	}
	for _, raw := range []string{"", "12:13:"} {
		v := sqltype.Null(d)
		if raw != "" {
			v, err = d.Input(context.Background(), raw, -1)
			if err != nil {
				t.Fatal(err)
			}
		}
		evaluation.Parameters[0] = v
		cursor, err := open.OpenSource(&evaluation)
		if err != nil {
			t.Fatal(err)
		}
		got, ok, err := cursor.Next(context.Background())
		if err != nil || ok || !got.IsNull() || got.Type() != node.ResultUsage().Type {
			t.Fatal("typed empty source", got, ok, err)
		}
		cursor.Close()
	}
	evaluation.Parameters[0] = input
	cursor, err := open.OpenSource(&evaluation)
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := cursor.Next(canceled); !errors.Is(err, context.Canceled) || !cursor.Exhausted || cursor.Value.Type() != nil {
		t.Fatal("canceled source retained payload", err)
	}
}

func TestLegacyTransactionIdentityBinding(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	for _, tc := range []struct {
		name, call string
		oid        uint32
		result     sqltype.ID
		volatility expr.Volatility
		parallel   byte
		formal     []sqltype.ID
	}{
		{"txid_current", "txid_current()", 2943, sqltype.Int8, expr.Stable, 'u', nil},
		{"txid_current_if_assigned", "txid_current_if_assigned()", 3348, sqltype.Int8, expr.Stable, 'u', nil},
		{"txid_status", "txid_status($1)", 3360, sqltype.Text, expr.Volatile, 's', []sqltype.ID{sqltype.Int8}},
	} {
		node, _, _ := strictNullUnitBind(t, e, op, scope, tc.call)
		legacySnapshotProtocols(t, node, tc.name, tc.oid, tc.result, tc.volatility, tc.parallel, false, tc.formal...)
		if node.(expr.Folding).PlanningFold() != expr.FoldNever {
			t.Fatal("runtime inquiry folded", tc.name)
		}
	}
	// Exercise actual signed formal evaluation at all raw bit boundaries through
	// visibility's shared bigint input, without inventing allocator values.
	node, b, evaluation := strictNullUnitBind(t, e, op, scope, "txid_visible_in_snapshot($1,$2)")
	idType, snapshotType := b.Params.Parameters[1].Type, b.Params.Parameters[2].Type
	snapshot, err := snapshotType.Input(context.Background(), "1:18446744073709551615:3", -1)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id   int64
		want bool
	}{{0, true}, {9223372036854775807, true}, {-9223372036854775808, true}, {-1, false}} {
		id, err := sqltype.Integer(idType, tc.id)
		if err != nil {
			t.Fatal(err)
		}
		evaluation.Parameters = []sqltype.Value{id, snapshot}
		v, err := node.Eval(&evaluation)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := v.Boolean(); err != nil || got != tc.want {
			t.Fatal("bit reinterpretation", tc, got, err)
		}
	}
}
