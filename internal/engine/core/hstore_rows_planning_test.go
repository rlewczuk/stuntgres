package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

func hstoreRowsRoutineUnitFixture(t *testing.T) (*core.Engine, *session.Operation, *core.StatementScope) {
	t.Helper()
	e, op, scope := jsonbRecordRoutineUnitFixture(t)
	// The shared fixture retains statement admission for private hosts. Release
	// it before setup SQL, then acquire a host containing the installed routines.
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION hstore")
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := scope.Finish(nil); err != nil {
			t.Error(err)
		}
	})
	result, err := scope.Execute(enginetest.Statement(t, "SELECT 1"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows != nil {
		if err := result.Rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return e, op, scope
}

// Exercise the selected declaration and shared planners, rather than substituting
// a fake source or permitting scalar Eval to return one member of a set.
func TestHstoreRowsSharedPlanning(t *testing.T) {
	e, op, scope := hstoreRowsRoutineUnitFixture(t)
	for _, name := range []string{"skeys", "svals", "each"} {
		for _, input := range []string{"'a=>NULL,b=>x'::hstore", "NULL::hstore", "''::hstore"} {
			t.Run(name+"/"+input, func(t *testing.T) {
				node, binder, evaluation := strictNullUnitBind(t, e, op, scope, name+"("+input+")")
				procedure, err := exec.ExpressionRoutineDescriptor(node)
				if err != nil || procedure == nil || !core.ProcedureReturnsSet(procedure) || exec.StrictScalarRoutine(procedure) {
					t.Fatal("selected set declaration classified as scalar", procedure, err)
				}
				for _, rewrite := range []func(*exec.Context, expr.Expr, bool) (expr.Expr, bool, error){exec.SimplifyConditional, exec.PreflightCoercionsWithPolicy} {
					local := evaluation
					next, _, err := rewrite(&local, node, false)
					call, ok := next.(functions.HstoreRows)
					if err != nil || !ok || !analyze.SameQuerySetOccurrence(node, call) || exec.ConditionalConstantInputs(call) {
						t.Fatal("planning lost real set occurrence", next, err)
					}
					requests := []expr.Expr{call}
					root, err := binder.LowerQuerySets(&plan.SingletonNode{}, requests, nil, false)
					if err != nil {
						t.Fatal(err)
					}
					stage, ok := root.(*plan.SetProjectNode)
					if !ok || len(stage.Calls) != 1 || !analyze.SameQuerySetOccurrence(call, stage.Calls[0]) || exec.SourceReturnsSet(requests[0]) {
						t.Fatal("shared target lifting lost occurrence", root)
					}
				}
			})
		}
	}
}

func TestHstoreRowsLiteralNullAdmission(t *testing.T) {
	e, op, scope := hstoreRowsRoutineUnitFixture(t)
	reader, done := enginetest.RoleOperation(t, e, "jsonb_authority_reader")
	defer done()
	for _, name := range []string{"skeys", "svals", "each"} {
		node, binder, evaluation := strictNullUnitBind(t, e, op, scope, name+"(NULL::hstore)")
		call := node.(functions.HstoreRows)
		if err := enginetest.SetObjectACL(e, context.Background(), call.Ref, authorization.ACLValue{Explicit: true}); err != nil {
			t.Fatal(err)
		}
		evaluation.RoutineAuthority = eMustJSONBRoutineAuthority(t, e, reader)
		evaluation.RoutineChecks = binder.Dependencies
		err := exec.AdmitRoutineCall(&evaluation, node, false, false)
		nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "42501", Message: "permission denied for function " + name})
	}
}
