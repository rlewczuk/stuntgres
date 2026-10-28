package enginetests_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestTriggerDepthExpression(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	node, _, evaluation := strictNullUnitBind(t, e, op, scope, "pg_trigger_depth()")
	n := node.(functions.TriggerDepth)
	d, err := exec.ExpressionRoutineDescriptor(n)
	if err != nil || d == nil || uint32(d.Address().ObjectID) != 3163 || !d.Strict || d.Volatility != expr.Stable || d.ReturnsSet() || n.ResultUsage().Type.ID() != sqltype.Int4 {
		t.Fatal("declaration", d, err)
	}
	if n.Validate() != nil || n.IntrinsicVolatility() != expr.Stable || n.PlanningFold() != expr.FoldNever || n.PreflightFold() != expr.PreflightKeep || len(n.Children()) != 0 || n.ClosedDefault(func(expr.Expr) bool { return true }) {
		t.Fatal("live leaf protocol", n)
	}
	before := n
	info := n.Info
	info.Occurrence++
	next, err := n.Rebuild(info, nil)
	if err != nil || next.(functions.TriggerDepth).Info != info || next.(functions.TriggerDepth).RoutineReference != n.RoutineReference || n != before {
		t.Fatal("provenance", next, err)
	}
	if _, err := n.Rebuild(info, []expr.Expr{n}); err == nil {
		t.Fatal("children accepted")
	}
	bad := n
	bad.Usage.Type = nil
	if bad.Validate() == nil {
		t.Fatal("nil result accepted")
	}
	text, _ := e.CatalogRead().LookupType(sqltype.Text)
	bad = n
	bad.Usage.Type = text
	if bad.Validate() == nil {
		t.Fatal("wrong result accepted")
	}
	retagged, changed, err := stored.RetagTypes(e.CatalogRead(), n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
	if err != nil || changed || !reflect.DeepEqual(retagged, n) {
		t.Fatal("retagged recipe", retagged, err)
	}
	rendered, err := n.Render()
	if err != nil || rendered.Name != "pg_trigger_depth" {
		t.Fatal(rendered, err)
	}
	capture, ok, err := n.Capture(expr.CaptureFunction)
	if err != nil || !ok || capture.Count != 0 {
		t.Fatal(capture, ok, err)
	}
	if !n.SameGroup(n, nil, false) || n.ValidateStoredProvider() != nil {
		t.Fatal("stored/group protocol")
	}
	// The same accepted leaf borrows a changing real invocation owner.
	h := scope.WriteHost()
	for i, want := range []int64{0, 1, 2, 1, 0} {
		if i == 1 || i == 2 {
			h.EnterForeignKeyTrigger()
		}
		if i == 3 || i == 4 {
			h.LeaveForeignKeyTrigger()
		}
		value, err := n.Eval(&evaluation)
		if err != nil || value.IsNull() || value.Type() != n.Usage.Type {
			t.Fatal(value, err)
		}
		v, err := value.Integer()
		if err != nil || v != want || n != before {
			t.Fatal("captured depth", v, want, err)
		}
	}
	cursor, err := n.OpenSource(&evaluation)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
	_, present, err := cursor.Next(context.Background())
	if err != nil || !present {
		t.Fatal("scalar source", present, err)
	}
	_, present, err = cursor.Next(context.Background())
	if err != nil || present {
		t.Fatal("scalar EOF", present, err)
	}
}
