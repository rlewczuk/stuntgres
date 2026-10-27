package enginetests_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestSettingExpressionBinding(t *testing.T) {
	e := engine.New()
	for _, tc := range []struct {
		source     string
		volatility expr.Volatility
		strict     expr.Strict
	}{
		{"current_setting('application_name')", expr.Stable, expr.StrictAfterBare},
		{"current_setting('application_name',true)", expr.Stable, expr.StrictAfterBare},
		{"current_setting('application_'||'name')", expr.Stable, expr.StrictAfterBare},
		{"current_setting(set_config('application_name','inside',false))", expr.Volatile, expr.StrictAfterBare},
		{"set_config('application_name','inside',false)", expr.Volatile, expr.StrictNone},
		{"current_setting(NULL::text)", expr.Stable, expr.StrictAfterBare},
		{"current_setting('missing.setting',true)", expr.Stable, expr.StrictAfterBare},
		{"set_config(NULL::text,'inside',false)", expr.Volatile, expr.StrictNone},
	} {
		b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		raw := enginetest.Statement(t, "SELECT 0,"+tc.source).(*parser.Select).Targets[1].(parser.FunctionExpr)
		node, err := b.BindFunctionProvider(raw)
		if err != nil {
			t.Fatal(err)
		}
		call, ok := node.(functions.Setting)
		if !ok || call.Usage.Type.ID() != sqltype.Text || call.RoutineReference == nil || call.Properties().Volatility != tc.volatility || call.StrictNull() != tc.strict || call.PlanningFold() != expr.FoldNever {
			t.Fatal("canonical setting declaration", node)
		}
		rebuilt, err := call.Rebuild(call.Info, call.Children())
		if err != nil {
			t.Fatal(err)
		}
		copy := rebuilt.(functions.Setting)
		for _, metadata := range []expr.ResultNullability{call, &call, copy, &copy} {
			for _, site := range []expr.ResultSite{expr.ResultSelect, expr.ResultReturning} {
				if nullable, known := metadata.ResultNullability(site); !nullable || !known {
					t.Fatalf("%s site %v: nullable=%t known=%t", tc.source, site, nullable, known)
				}
			}
		}
		if &copy.Args[0] == &call.Args[0] || !call.SameGroup(copy, func(a, b expr.Expr) bool { return reflect.DeepEqual(a, b) }, true) {
			t.Fatal("setting rebuild lost immutable child ownership")
		}
		rendering, err := call.Render()
		if err != nil {
			t.Fatal(err)
		}
		if rendering.Replay.Validate == nil {
			t.Fatal("missing retained signature validation")
		}
		if err := rendering.Replay.Validate(); err != nil {
			t.Fatal(err)
		}
		query, err := b.BindSelect(&parser.Select{Targets: []parser.Expr{raw}}, false)
		if err != nil {
			t.Fatal(err)
		}
		columns := query.Root.Schema()
		if len(columns) != 1 || !columns[0].Nullable || !columns[0].NullabilityKnown || columns[0].OriginTable != 0 || columns[0].OriginAttribute != 0 || columns[0].Usage.Type.ID() != sqltype.Text {
			t.Fatalf("%s result metadata: %#v", tc.source, columns)
		}
	}
}
