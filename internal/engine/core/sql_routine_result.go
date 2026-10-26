package core

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/proc"
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

func SQLRoutineFinalError(result sqltype.Usage, names ...func(sqltype.ID) string) error {
	var name func(sqltype.ID) string
	if len(names) != 0 {
		name = names[0]
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P13", Message: "return type mismatch in function declared to return " + proc.ResultDiagnosticName(result.Type, name), Detail: "Function's final statement must be SELECT or INSERT/UPDATE/DELETE/MERGE RETURNING."}
}

func ValidateSQLRoutineTerminal(statement parser.Statement, b analyze.Binding, result sqltype.Usage, auth AuthorizationView) (proc.ResultPlan, error) {
	// Naming is error-only. Successful execution does not acquire a display
	// snapshot or retain a callback in the immutable row-adaptation plan.
	name := b.Resolvers.TypeDiagnosticName
	var namingErr error
	display := func(id sqltype.ID) string {
		if name == nil && namingErr == nil {
			name, namingErr = CaptureTypeDiagnosticName(auth)
		}
		if name != nil {
			return name(id)
		}
		return ""
	}
	var plan proc.ResultPlan
	var err error
	if _, selecting := statement.(*parser.Select); selecting && b.Query != nil {
		plan, err = validateSQLRoutineResult(b.Query.Root.Schema(), result, display)
	} else if b.Returning != nil {
		plan, err = validateSQLRoutineResult(b.Returning.Schema, result, display)
	} else {
		err = SQLRoutineFinalError(result, display)
	}
	if namingErr != nil {
		return proc.ResultPlan{}, namingErr
	}
	return plan, err
}

func validateSQLRoutineResult(columns []catalog.Column, result sqltype.Usage, names ...func(sqltype.ID) string) (proc.ResultPlan, error) {
	usages := make([]sqltype.Usage, len(columns))
	for i, column := range columns {
		usages[i] = column.Usage
	}
	return proc.PlanResult(usages, result, names...)
}
