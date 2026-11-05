package enginetests_test

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestBackendStateBinding(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	for i, oid := range []uint32{1940, 2094, 2857, 2853, 2788, 6107} {
		kind := functions.BackendStateKind(i)
		node, _, evaluation := strictNullUnitBind(t, e, op, scope, kind.Name()+"(NULL::integer)")
		n, ok := node.(functions.BackendState)
		if !ok || n.Kind != kind || n.Validate() != nil || n.ValidateStoredProvider() != nil {
			t.Fatal("typed recipe", node)
		}
		d, err := exec.ExpressionRoutineDescriptor(n)
		if err != nil || d == nil || uint32(d.Address().ObjectID) != oid || !d.Strict || d.ReturnsSet() || d.Volatility != expr.Stable || d.Builtin.Parallel != 'r' || d.RowEstimate() != 0 || d.CostEstimate() != 1 || string(d.Signature) != "23" {
			t.Fatal("exact scalar declaration", d, err)
		}
		if n.ResultUsage().Type.ID() != kind.ResultType() || !exec.SourceAdmitted(n) || exec.SourceReturnsSet(n) || n.PlanningFold() != expr.FoldNever || n.StrictNull() != expr.StrictAfterBare {
			t.Fatal("scalar policy", n)
		}
		if kind == functions.BackendStateSubxact {
			if d.OutputDeclaration() == nil || n.Usage.Type.CompositeLen() != 2 {
				t.Fatal("fixed OUT shape")
			}
			for j, name := range []string{"subxact_count", "subxact_overflowed"} {
				if n.Usage.Type.CompositeMember(j).Name != name {
					t.Fatal("OUT names")
				}
			}
		} else if d.OutputDeclaration() != nil || d.InputNames != nil {
			t.Fatal("invented input or output names")
		}
		v, err := n.Eval(&evaluation)
		if err != nil || !v.IsNull() || v.Type() != n.Usage.Type {
			t.Fatal("whole typed strict NULL", v, err)
		}
		cursor, err := n.OpenSource(&evaluation)
		if err != nil {
			t.Fatal(err)
		}
		v, present, err := cursor.Next(context.Background())
		if err != nil || !present || !v.IsNull() {
			t.Fatal("scalar FROM NULL cardinality", v, present, err)
		}
		if _, present, err := cursor.Next(context.Background()); err != nil || present {
			t.Fatal("extra scalar row", err)
		}
		cursor.Close()
		folded, _, err := exec.PreflightCoercions(&evaluation, n)
		if err != nil {
			t.Fatal(err)
		}
		_, remains := folded.(functions.BackendState)
		if remains != (kind == functions.BackendStateSubxact) {
			t.Fatal("scalar NULL fold versus record ACL admission", folded)
		}
		before := n
		for _, rebuild := range []func(expr.Info, []expr.Expr) (expr.Expr, error){n.Rebuild, n.Substitute} {
			next, err := rebuild(n.Info, n.Children())
			if err != nil || !reflect.DeepEqual(next, n) {
				t.Fatal("reconstruction lost provenance", next, err)
			}
			if _, err := rebuild(n.Info, nil); err == nil {
				t.Fatal("invalid reconstruction arity")
			}
			if _, err := rebuild(n.Info, []expr.Expr{nil}); err == nil {
				t.Fatal("missing reconstruction operand")
			}
		}
		if render, err := n.Render(); err != nil || render.Name != kind.Name() || len(render.Args) != 1 {
			t.Fatal("render protocol", render, err)
		}
		if capture, ok, err := n.Capture(expr.CaptureFunction); err != nil || !ok || capture.Count != 1 || !capture.Indexed {
			t.Fatal("capture protocol", capture, err)
		}
		if child, err := n.CaptureOperand(0); err != nil || child != n.Argument {
			t.Fatal("capture original operand", err)
		}
		if _, err := n.CaptureOperand(1); err == nil {
			t.Fatal("capture out of range")
		}
		var jumble ginCleanupJumble
		n.JumbleFields(&jumble)
		if !reflect.DeepEqual(jumble.fields, []string{"functions.BackendState"}) || !n.SameSourceOccurrence(n) || !n.SameGroup(n, func(a, b expr.Expr) bool { return reflect.DeepEqual(a, b) }, false) {
			t.Fatal("identity protocols")
		}
		if _, changed, err := stored.RetagTypes(e.CatalogRead(), n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }); err != nil || changed {
			t.Fatal("stored provider identity", changed, err)
		}
		destination := *n.Usage.Type
		info := n.Info
		info.Usage = destination.Usage()
		next, err := n.Rebuild(info, n.Children())
		if err != nil || next.ResultUsage().Type != &destination || next.(functions.BackendState).RoutineReference != n.RoutineReference || !reflect.DeepEqual(n, before) {
			t.Fatal("destination owned type/provenance", next, err)
		}
		bad := n
		bad.Kind = 255
		if bad.Validate() == nil {
			t.Fatal("invalid kind")
		}
		bad = n
		bad.RoutineReference = nil
		if bad.Validate() == nil {
			t.Fatal("missing routine authority")
		}
		bad = n
		bad.Argument = n
		if bad.Validate() == nil {
			t.Fatal("invalid operand type")
		}
		nonnull, _, evaluation := strictNullUnitBind(t, e, op, scope, kind.Name()+"(0)")
		live, _, err := exec.PreflightCoercions(&evaluation, nonnull)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := live.(functions.BackendState); !ok {
			t.Fatal("stable runtime recipe folded", live)
		}
	}
}
