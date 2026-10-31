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

func TestStatsSnapshotExpressionAuthority(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	for _, tc := range []struct {
		name          string
		oid           uint32
		id            sqltype.ID
		clear, strict bool
		volatility    expr.Volatility
	}{{"pg_stat_get_snapshot_timestamp", 3788, sqltype.Timestamptz, false, true, expr.Stable}, {"pg_stat_clear_snapshot", 2230, sqltype.Void, true, false, expr.Volatile}} {
		node, _, evaluation := strictNullUnitBind(t, e, op, scope, tc.name+"()")
		n := node.(functions.StatsSnapshot)
		declaration, err := exec.ExpressionRoutineDescriptor(n)
		if err != nil || declaration == nil || uint32(declaration.Address().ObjectID) != tc.oid || declaration.Strict != tc.strict || declaration.Volatility != tc.volatility || declaration.ReturnsSet() || n.ResultUsage().Type.ID() != tc.id {
			t.Fatal("routine declaration", declaration, err)
		}
		if n.Clear != tc.clear || n.Validate() != nil || n.IntrinsicVolatility() != tc.volatility || n.PlanningFold() != expr.FoldNever || n.PreflightFold() != expr.PreflightKeep || n.ClosedDefault(func(expr.Expr) bool { return true }) || len(n.Children()) != 0 {
			t.Fatal("leaf protocol", n)
		}
		before := n
		info := n.Info
		info.Occurrence++
		next, err := n.Rebuild(info, nil)
		if err != nil || next.(functions.StatsSnapshot).Info != info || next.(functions.StatsSnapshot).RoutineReference != n.RoutineReference || n != before {
			t.Fatal("recipe provenance", next, err)
		}
		if _, err := n.Rebuild(info, []expr.Expr{n}); err == nil {
			t.Fatal("accepted children")
		}
		retagged, changed, err := stored.RetagTypes(e.CatalogRead(), n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
		if err != nil || changed || !reflect.DeepEqual(retagged, n) {
			t.Fatal("immutable retag", retagged, err)
		}
		render, err := n.Render()
		if err != nil || render.Name != tc.name {
			t.Fatal(render, err)
		}
		capture, ok, err := n.Capture(expr.CaptureFunction)
		if err != nil || !ok || capture.Count != 0 {
			t.Fatal(capture, ok, err)
		}
		bad := n
		bad.Usage.Type = nil
		if bad.Validate() == nil {
			t.Fatal("nil result accepted")
		}
		cursor, err := n.OpenSource(&evaluation)
		if err != nil {
			t.Fatal(err)
		}
		v, present, err := cursor.Next(context.Background())
		if err != nil || !present || v.IsNull() == tc.clear || v.Type() != n.Usage.Type {
			t.Fatal("typed scalar result", v, present, err)
		}
		_, present, err = cursor.Next(context.Background())
		if err != nil || present {
			t.Fatal("scalar EOF", present, err)
		}
		cursor.Close()
		var detached exec.Context
		if _, err := n.Eval(&detached); err == nil {
			t.Fatal("missing live authority accepted")
		}
		if n != before {
			t.Fatal("evaluation captured authority on immutable recipe")
		}
	}
}
