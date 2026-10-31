package enginetests_test

import (
	"reflect"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestSessionSignalTypedRecipes(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	for i, call := range []string{"pg_cancel_backend(NULL::integer)", "pg_terminate_backend(NULL::integer)", "pg_terminate_backend(timeout=>0,pid=>NULL::integer)"} {
		node, _, evaluation := strictNullUnitBind(t, e, op, scope, call)
		n, ok := node.(functions.SessionSignal)
		if !ok {
			t.Fatalf("recipe %T", node)
		}
		oid := ids.OID(2096)
		if i == 0 {
			oid = 2171
		}
		d, err := exec.ExpressionRoutineDescriptor(n)
		if err != nil || d == nil || d.Address().ObjectID != oid || d.Result != 16 || !d.Strict || d.Volatility != expr.Volatile || d.Builtin.Parallel != 's' || d.ReturnsSet() {
			t.Fatal(d, err)
		}
		if d.ACL.Explicit {
			t.Fatal("PUBLIC EXECUTE default replaced", d.ACL)
		}
		if n.Validate() != nil || n.Usage.Type.ID() != sqltype.Bool || n.PlanningFold() != expr.FoldNever || n.PreflightFold() != expr.PreflightNullBare || n.StrictNull() != expr.StrictAfterBare {
			t.Fatal(n)
		}
		before := n
		info := n.Info
		info.Occurrence++
		rebuilt, err := n.Rebuild(info, n.Children())
		if err != nil || rebuilt.(functions.SessionSignal).RoutineReference != n.RoutineReference || !reflect.DeepEqual(before, n) {
			t.Fatal(rebuilt, err)
		}
		rendering, err := n.Render()
		if err != nil {
			t.Fatal(err)
		}
		if i == 2 && (rendering.Args[0].Label != "timeout" || rendering.Args[1].Label != "pid") {
			t.Fatal(rendering)
		}
		capture, ok, err := n.Capture(expr.CaptureFunction)
		if err != nil || !ok || capture.Count != int(n.Arity) {
			t.Fatal(capture, ok, err)
		}
		for j, child := range n.Children() {
			captured, err := n.CaptureOperand(j)
			if err != nil || captured != child {
				t.Fatal(captured, err)
			}
		}
		value, err := n.Eval(&evaluation)
		if err != nil || !value.IsNull() || value.Type() != n.Usage.Type {
			t.Fatal(value, err)
		}
		children := n.Children()
		children[0] = nil
		if n.Validate() != nil {
			t.Fatal("child slice mutated recipe")
		}
		if _, err := n.Rebuild(info, children); err == nil {
			t.Fatal("nil child accepted")
		}
		bad := n
		bad.Kind = 255
		if bad.Validate() == nil {
			t.Fatal("invalid kind accepted")
		}
	}
}
