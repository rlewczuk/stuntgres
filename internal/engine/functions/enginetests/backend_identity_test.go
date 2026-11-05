package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestBackendIdentityBinding(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	cases := []struct {
		kind functions.BackendIdentityKind
		oid  uint32
		typ  sqltype.ID
	}{
		{functions.BackendIDSet, 1936, sqltype.Int4}, {functions.BackendPID, 1937, sqltype.Int4}, {functions.BackendDatabaseID, 1938, sqltype.Oid}, {functions.BackendUserID, 1939, sqltype.Oid}, {functions.BackendStart, 1391, sqltype.Timestamptz}, {functions.BackendClientAddress, 1392, sqltype.Inet}, {functions.BackendClientPort, 1393, sqltype.Int4},
	}
	for _, tc := range cases {
		text := tc.kind.Name() + "(NULL::integer)"
		if tc.kind == functions.BackendIDSet {
			text = tc.kind.Name() + "()"
		}
		node, _, evaluation := strictNullUnitBind(t, e, op, scope, text)
		n, ok := node.(functions.BackendIdentity)
		if !ok || n.Kind != tc.kind {
			t.Fatalf("genuine typed identity recipe %T", node)
		}
		d, err := exec.ExpressionRoutineDescriptor(n)
		set := tc.kind == functions.BackendIDSet
		rows, signature := float32(0), "23"
		if set {
			rows, signature = 100, ""
		}
		if err != nil || d == nil || uint32(d.Address().ObjectID) != tc.oid || !d.Strict || d.Volatility != expr.Stable || d.ReturnsSet() != set || sqltype.ID(d.Result) != tc.typ || string(d.Signature) != signature || d.Builtin.Parallel != 'r' || d.RowEstimate() != rows || d.CostEstimate() != 1 || d.OutputDeclaration() != nil || d.InputNames != nil {
			t.Fatal("released scalar/set declaration", tc.kind, d, err)
		}
		if n.Validate() != nil || n.ValidateStoredProvider() != nil || n.ResultUsage().Type.ID() != tc.typ || !exec.SourceAdmitted(n) || exec.SourceReturnsSet(n) != set || n.PlanningFold() != expr.FoldNever {
			t.Fatal("typed immutable recipe properties", n)
		}
		before := n
		var jumble ginCleanupJumble
		n.JumbleFields(&jumble)
		if !reflect.DeepEqual(jumble.fields, []string{"functions.BackendIdentity"}) {
			t.Fatal("owned statement-jumble discriminator", jumble)
		}
		if !set {
			v, err := n.Eval(&evaluation)
			if err != nil || !v.IsNull() || v.Type() != n.Usage.Type {
				t.Fatal("typed strict scalar NULL", v, err)
			}
			cursor, err := n.OpenSource(&evaluation)
			if err != nil || cursor.Type != n.Usage.Type {
				t.Fatal("scalar FROM NULL metadata", cursor, err)
			}
			if v, present, err := cursor.Next(context.Background()); err != nil || !present || !v.IsNull() || v.Type() != n.Usage.Type {
				t.Fatal("one scalar NULL row", v, present, err)
			}
			if _, present, err := cursor.Next(context.Background()); err != nil || present {
				t.Fatal("scalar FROM emitted more than one row", present, err)
			}
			cursor.Close()
			folded, _, err := exec.PreflightCoercions(&evaluation, n)
			if err != nil {
				t.Fatal(err)
			}
			if _, remains := folded.(functions.BackendIdentity); remains {
				t.Fatal("ordinary strict NULL planning was disabled")
			}
			v, err = folded.Eval(&evaluation)
			if err != nil || !v.IsNull() || v.Type() != n.Usage.Type {
				t.Fatal("folded typed strict result", v, err)
			}
		}
		children := n.Children()
		rebuilt, err := n.Rebuild(n.Info, children)
		if err != nil || !reflect.DeepEqual(rebuilt, n) {
			t.Fatal("immutable rebuild", rebuilt, err)
		}
		if _, err := n.Rebuild(n.Info, append(children, n)); err == nil {
			t.Fatal("bad reconstruction arity")
		}
		if render, err := n.Render(); err != nil || render.Name != tc.kind.Name() || len(render.Args) != len(children) {
			t.Fatal("ordinary call rendering", render, err)
		}
		capture, ok, err := n.Capture(expr.CaptureFunction)
		if err != nil || !ok || capture.Count != len(children) {
			t.Fatal("capture operands", capture, ok, err)
		}
		if len(children) == 1 {
			if child, err := n.CaptureOperand(0); err != nil || child != n.Argument {
				t.Fatal("original child provenance", child, err)
			}
		}
		if _, err := n.CaptureOperand(len(children)); err == nil {
			t.Fatal("out-of-range capture admitted")
		}
		if !n.SameGroup(n, func(a, b expr.Expr) bool { return reflect.DeepEqual(a, b) }, false) || !n.SameSourceOccurrence(n) {
			t.Fatal("group/source identity")
		}
		if _, changed, err := stored.RetagTypes(e.CatalogRead(), n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }); err != nil || changed {
			t.Fatal("identity retag", changed, err)
		}
		// Destination usages are rebuilt, never written into the accepted source.
		destination := *n.Usage.Type
		info := n.Info
		info.Usage = destination.Usage()
		next, err := n.Rebuild(info, children)
		if err != nil || next.ResultUsage().Type != &destination || !reflect.DeepEqual(before, n) {
			t.Fatal("destination result generation", next, err)
		}
		if next.(functions.BackendIdentity).RoutineReference != n.RoutineReference {
			t.Fatal("retag replaced original routine authority")
		}
		bad := n
		bad.Kind = functions.BackendIdentityKind(255)
		if bad.Validate() == nil {
			t.Fatal("open-ended family discriminator")
		}
		if !reflect.DeepEqual(before, n) {
			t.Fatal("protocol mutated retained recipe")
		}
	}
	enginetest.StartSession(t, e, nil)
	node, _, evaluation := strictNullUnitBind(t, e, op, scope, "pg_stat_get_backend_idset()")
	n := node.(functions.BackendIdentity)
	cursor, err := n.OpenSource(&evaluation)
	if err != nil || len(cursor.Values) < 2 {
		t.Fatal("actual detached ID cursor", cursor, err)
	}
	first, present, err := cursor.Next(context.Background())
	if err != nil || !present || first.IsNull() || first.Type() != n.Usage.Type {
		t.Fatal(first, present, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if value, present, err := cursor.Next(ctx); !errors.Is(err, context.Canceled) || present || value.Type() != nil || cursor.Values != nil || !cursor.Exhausted {
		t.Fatal("cursor cancellation did not release pending IDs", cursor, err)
	}
	if _, err := first.Integer(); err != nil {
		t.Fatal("cancellation mutated returned scalar", err)
	}
	cursor, err = n.OpenSource(&evaluation)
	if err != nil {
		t.Fatal(err)
	}
	cursor.Close()
	if cursor.Values != nil || !cursor.Exhausted {
		t.Fatal("Close retained detached IDs", cursor)
	}
}
