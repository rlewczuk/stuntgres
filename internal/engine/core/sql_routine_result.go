package core

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func RoutineCommandTag(statement parser.Statement) string {
	if transaction, ok := statement.(*parser.TransactionControl); ok {
		switch transaction.Kind {
		case parser.KindCommit:
			return "COMMIT"
		case parser.KindStart:
			return "BEGIN"
		case parser.KindRollback:
			return "ROLLBACK"
		}
	}
	tag, _, _, _ := session.ClassifyCommandTag(statement, session.TxIdle)
	return tag
}

func SQLRoutineFinalError(result sqltype.Usage) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P13", Message: "return type mismatch in function declared to return " + result.Type.DiagnosticName(), Detail: "Function's final statement must be SELECT or INSERT/UPDATE/DELETE/MERGE RETURNING."}
}

func ValidateSQLRoutineTerminal(statement parser.Statement, b analyze.Binding, result sqltype.Usage) (sqltype.CoercionPlan, error) {
	if _, selecting := statement.(*parser.Select); selecting && b.Query != nil {
		return validateSQLRoutineResult(b.Query.Root.Schema(), result)
	}
	if b.Returning != nil {
		return validateSQLRoutineResult(b.Returning.Schema, result)
	}
	return sqltype.CoercionPlan{}, SQLRoutineFinalError(result)
}

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
