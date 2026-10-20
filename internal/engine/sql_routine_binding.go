package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (e *Engine) executeCreateFunction(scope *StatementScope, s *parser.CreateFunction, count int) (Result, error) {
	result, err := e.executeSchema(scope, scope.op, s, count, scope.input)
	if err == nil {
		scope.completed = true
	}
	return result, err
}

// Binding is deliberately producer-free. In particular this does not evaluate
// constants/providers or take a data snapshot while validating CREATE.
func (e *Engine) bindSQLRoutineLocked(op *session.Operation, auth AuthorizationView, d *catalog.Procedure, input sqltype.InputEnvironment, scope *StatementScope) (analyze.Binding, sqltype.CoercionPlan, error) {
	var admission *analyze.LockAdmission
	var expected *StatementDescription
	if scope != nil {
		admission = &scope.queryLockAdmission
		expected = scope.Expected
	}
	b, err := analyze.Describe(op.Context(), e.analysisHost(op), d.SQL.Query, authorityOf(auth), ids.ObjectRef{}, input, expectedOf(expected), admission, nil, true, d.SQL.Inputs)
	if err != nil {
		return b, sqltype.CoercionPlan{}, exec.SQLRoutineContext(err, d.Name(), false)
	}
	plan, err := validateSQLRoutineResult(b.Query.Root.Schema(), d.SQL.Result)
	return b, plan, exec.SQLRoutineContext(err, d.Name(), false)
}

// The same producer-free contract applies to CREATE/Restore and the actual
// runtime binding before CTE preparation or query opening.
func validateSQLRoutineResult(columns []catalog.Column, result sqltype.Usage) (sqltype.CoercionPlan, error) {
	mismatch := func(detail string) error {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P13", Message: "return type mismatch in function declared to return " + result.Type.DiagnosticName(), Detail: detail}
	}
	if len(columns) != 1 {
		return sqltype.CoercionPlan{}, mismatch("Final statement must return exactly one column.")
	}
	plan, err := sqltype.PlanCoercion(columns[0].Usage, result, sqltype.Assignment)
	if err != nil {
		return plan, mismatch("Actual return type is " + columns[0].Usage.Type.DiagnosticName() + ".")
	}
	return plan, nil
}
