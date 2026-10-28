package enginetests_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestSettingsOptionsBinding(t *testing.T) {
	e := engine.New()
	b := enginetest.ExpressionBinder(t,context.Background(),e,sqltype.InputEnvironment{},nil)
	raw := enginetest.Statement(t,`SELECT pg_settings_get_flags('work_mem')`).(*parser.Select).Targets[0].(parser.FunctionExpr)
	node,err := b.BindFunctionProvider(raw)
	if err != nil { t.Fatal(err) }
	flags,ok := node.(functions.Setting)
	if !ok || flags.Usage.Type.ID() != 1009 || flags.RoutineReference == nil || flags.RoutineReference.RoutineOID() != 6240 || flags.StrictNull() != expr.StrictAfterBare || flags.Properties().Volatility != expr.Stable || flags.IntrinsicVolatility() != expr.Stable || flags.PlanningFold() != expr.FoldNever { t.Fatal(node) }
	rebuilt,err := flags.Rebuild(flags.Info,flags.Children())
	if err != nil || &rebuilt.(functions.Setting).Args[0] == &flags.Args[0] { t.Fatal(rebuilt,err) }
	render,err := flags.Render()
	if err != nil || render.Name != "pg_settings_get_flags" || render.Replay.Validate() != nil { t.Fatal(render,err) }
	capture,ok,err := flags.Capture(expr.CaptureFunction)
	if err != nil || !ok || capture.Count != 1 || capture.Check(flags,1) != nil { t.Fatal(capture,ok,err) }
	if operand,err := flags.CaptureOperand(0); err != nil || !reflect.DeepEqual(operand,flags.Args[0]) { t.Fatal(operand,err) }
	broken := flags
	broken.Usage = b.Type(sqltype.Text).Usage()
	invalid,_ := broken.Render()
	if invalid.Replay.Validate() == nil { t.Fatal("accepted stale TEXT setting result") }

	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth,err := e.AuthorizationLocked(binderOperation(b))
	if err != nil { t.Fatal(err) }
	b.Query.Auth,b.Query.Frame = &auth,1
	for _,source := range []string{`pg_options_to_table(ARRAY['x=1'])`,`pg_options_to_table(options_array => ARRAY['x=1'])`} {
		raw := enginetest.Statement(t,"SELECT "+source).(*parser.Select).Targets[0].(parser.FunctionExpr)
		bound,ns,err := b.BindFrom(&parser.FunctionTable{Calls:[]parser.FunctionExpr{raw}})
		if err != nil { t.Fatal(err) }
		from,ok := bound.(*plan.FunctionFromNode)
		if !ok || len(from.Calls) != 1 || len(ns.Columns) != 2 { t.Fatal(bound,ns) }
		options,ok := from.Calls[0].(functions.OptionsToTable)
		if !ok || options.Validate() != nil || options.RoutineReference == nil || options.RoutineReference.RoutineOID() != 2289 || options.StrictNull() != expr.StrictAfterBare || options.IntrinsicVolatility() != expr.Stable || options.Properties().Volatility != expr.Stable || options.PlanningFold() != expr.FoldNever { t.Fatal(from.Calls[0]) }
		for i,name := range []string{"option_name","option_value"} {
			if ns.Columns[i].Descriptor.Name != name || ns.Columns[i].Descriptor.Usage.Type.ID() != sqltype.Text { t.Fatal(ns.Columns) }
			column := ns.Columns[i].Descriptor
			if column.Nullable || column.NullabilityKnown || column.OriginTable != 0 || column.OriginAttribute != 0 {
				t.Fatal("computed OUT member must not claim table nullability or origin", column)
			}
		}
		copy,err := options.Rebuild(options.Info,options.Children())
		if err != nil || !options.SameSourceOccurrence(copy) || !options.SameGroup(copy,func(a,b expr.Expr) bool { return reflect.DeepEqual(a,b) },true) { t.Fatal(copy,err) }
		render,err := options.Render()
		if err != nil || render.Replay.Validate() != nil || render.Name != "pg_options_to_table" { t.Fatal(render,err) }
		capture,ok,err := options.Capture(expr.CaptureFunction)
		if err != nil || !ok || capture.Count != 1 || capture.Check(options,1) != nil { t.Fatal(capture,ok,err) }
		if operand,err := options.CaptureOperand(0); err != nil || !reflect.DeepEqual(operand,options.Arg) { t.Fatal(operand,err) }
	}
	seen := 0
	for _,d := range routine.BuiltinRoutineDeclarations {
		if d.Identity.ID != 2289 && d.Identity.ID != 6240 { continue }
		seen++
		if !d.Strict || d.Volatility != expr.Stable || d.Parallel != 's' || d.Providers != functions.SettingRoutineProvider || d.Variadic != 0 || d.Defaults != 0 { t.Fatal(d) }
		if d.Identity.ID == 6240 {
			if d.Result != 1009 || d.ReturnsSet || d.Outputs != nil { t.Fatal(d) }
		} else {
			if d.Result != 2249 || !d.ReturnsSet || d.Outputs == nil || d.Outputs.Len() != 2 || d.Outputs.Inputs() != 1 { t.Fatal(d) }
			for i,name := range []string{"options_array","option_name","option_value"} { if d.Outputs.Name(i) != name { t.Fatal(d.Outputs) } }
			for i := range 2 { if d.Outputs.Type(i) != 25 { t.Fatal(d.Outputs) } }
		}
	}
	if seen != 2 { t.Fatal("duplicate/missing declarations",seen) }
}
