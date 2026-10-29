package core_test

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// Generic parameter NULL must preserve the genuine SQL/PL call and its original
// manifest for current EXECUTE admission. Custom planning may eliminate it;
// literal NULL remains eliminable in both modes. No body runs during this proof.
func TestRoutineGenericParameterNullDemand(t *testing.T) {
	for _, language := range []string{"sql", "plpgsql"} {
		t.Run(language, func(t *testing.T) {
			e, op, scope := strictNullUnitFixture(t)
			if err := scope.Finish(nil); err != nil {
				t.Fatal(err)
			}
			body := "SELECT v"
			if language == "plpgsql" {
				body = "BEGIN RETURN v; END"
			}
			enginetest.RoleExecute(t, e, op, "CREATE FUNCTION parameter_demand(v integer) RETURNS integer LANGUAGE "+language+" STRICT AS $$"+body+"$$")
			var err error
			scope, err = e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(nil)
			arrayStrictNullAdmitScope(t, scope)
			for _, planner := range arrayStrictNullPlanners {
				for _, generic := range []bool{false, true} {
					for _, argument := range []string{"NULL::integer", "$1::integer"} {
						node, binder, evaluation := strictNullUnitBind(t, e, op, scope, "parameter_demand("+argument+")")
						call, ok := node.(plan.SQLRoutineExpr)
						if !ok || call.Procedure.Host != nil || call.Procedure.SQL == nil {
							t.Fatal("not a genuine nonhost routine call", node)
						}
						before := arrayStrictNullSnapshot(t, node)
						evaluation.PreserveParameterDemand = generic
						if argument == "$1::integer" {
							evaluation.Parameters = []sqltype.Value{sqltype.Null(binder.Params.Parameters[1].Type)}
						}
						next, changed, err := planner.run(&evaluation, node, true)
						if err != nil {
							t.Fatal(planner.name, generic, argument, err)
						}
						if generic && argument == "$1::integer" {
							if changed || !reflect.DeepEqual(next, before) {
								t.Fatal("generic NULL erased declaration/children/manifest", planner.name, next)
							}
						} else {
							if !changed {
								t.Fatal("literal/custom NULL retained callable", planner.name)
							}
							arrayStrictNullResult(t, node, next)
						}
						if !reflect.DeepEqual(node, before) {
							t.Fatal("planning mutated original call")
						}
					}
				}
			}
		})
	}
}
