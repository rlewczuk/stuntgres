package enginetests_test

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestTriggerBuiltinsRecipes(t *testing.T) {
	for _, tc := range []struct {
		name, code, message, source string
		oid                         uint32
		strict                      bool
	}{
		{"suppress_redundant_updates_trigger", "39P01", "suppress_redundant_updates_trigger: must be called as trigger", "suppress_redundant_updates_trigger", 1291, true},
		{"tsvector_update_trigger", "XX000", "tsvector_update_trigger: not fired by trigger manager", "tsvector_update_trigger_byid", 3752, false},
		{"tsvector_update_trigger_column", "XX000", "tsvector_update_trigger: not fired by trigger manager", "tsvector_update_trigger_bycolumn", 3753, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, op, scope := strictNullUnitFixture(t)
			node, _, evaluation := strictNullUnitBind(t, e, op, scope, "pg_catalog."+tc.name+"()")
			n := node.(functions.TriggerBuiltin)
			d, err := exec.ExpressionRoutineDescriptor(n)
			if err != nil || d == nil || uint32(d.Address().ObjectID) != tc.oid || d.Result != 2279 || d.Strict != tc.strict || d.Volatility != expr.Volatile || d.Builtin.Parallel != 's' || d.Cost != 1 || d.Display.Source != tc.source || d.SQL != nil {
				t.Fatal("real declaration", d, err)
			}
			if n.Validate() != nil || n.PlanningFold() != expr.FoldNever || n.PreflightFold() != expr.PreflightKeep || n.IntrinsicVolatility() != expr.Volatile || len(n.Children()) != 0 {
				t.Fatal("recipe protocol")
			}
			info := n.Info
			info.Occurrence++
			next, err := n.Rebuild(info, nil)
			if err != nil || next.(functions.TriggerBuiltin).RoutineReference != n.RoutineReference || next.(functions.TriggerBuiltin).Info != info {
				t.Fatal("rebuilt provenance", next, err)
			}
			if _, err := n.Rebuild(info, []expr.Expr{n}); err == nil {
				t.Fatal("accepted hidden children")
			}
			retagged, changed, err := stored.RetagTypes(e.CatalogRead(), n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
			if err != nil || changed || !reflect.DeepEqual(retagged, n) {
				t.Fatal("stored recipe ownership", retagged, err)
			}
			render, err := n.Render()
			if err != nil || render.Name != tc.name {
				t.Fatal(render, err)
			}
			capture, ok, err := n.Capture(expr.CaptureFunction)
			if err != nil || !ok || capture.Count != 0 {
				t.Fatal(capture, ok, err)
			}
			before := n
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message}
			for _, nested := range []bool{false, true} {
				if nested {
					scope.WriteHost().EnterForeignKeyTrigger()
				}
				_, err := n.Eval(&evaluation)
				if nested {
					scope.WriteHost().LeaveForeignKeyTrigger()
				}
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || *diagnostic != want || n != before {
					t.Fatal("direct call borrowed activation or changed diagnostic", err)
				}
			}
			_, err = n.OpenSource(&evaluation)
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || *diagnostic != want {
				t.Fatal("direct source protocol diagnostic", err)
			}
		})
	}
}
