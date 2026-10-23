package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (e *Engine) executeSQLRoutineDDL(scope *StatementScope, s parser.Statement, count int) (Result, error) {
	result, err := e.executeSchema(scope, scope.op, s, count, scope.input)
	if err == nil {
		scope.completed = true
	}
	return result, err
}

// CREATE and Restore inspect the complete body without opening any producer.
// Runtime text binding uses only the reached command, never this validator.
func (e *Engine) bindSQLRoutineLocked(op *session.Operation, auth AuthorizationView, d *catalog.Procedure, input sqltype.InputEnvironment, scope *StatementScope) (last analyze.Binding, resultPlan sqltype.CoercionPlan, err error) {
	if d.SQLBody != nil {
		if err := d.SQLBody.ValidateDefinition(d); err != nil {
			return last, resultPlan, err
		}
		return last, resultPlan, d.SQLBody.Validate(auth.Catalog)
	}
	var actions []stored.RuleAction
	if d.SQL.Standard {
		actions = make([]stored.RuleAction, 0, len(d.SQL.Statements))
	}
	for i, statement := range d.SQL.Statements {
		if !sqlRoutineDataCommand(statement) {
			if d.SQL.Standard {
				return last, resultPlan, sqlError("0A000", routineCommandTag(statement)+" is not yet supported in unquoted SQL function body")
			}
			// Transaction statements are syntactically valid in quoted bodies;
			// PostgreSQL rejects them only when reached at execution.
			last = analyze.Binding{}
		} else {
			var target parser.ObjectName
			switch syntax := statement.(type) {
			case *parser.Insert:
				target = syntax.Name
			case *parser.Update:
				target = syntax.Name
			case *parser.Delete:
				target = syntax.Name
			case *parser.Merge:
				target = syntax.Name
			}
			var required ids.ObjectRef
			if len(target.Parts) != 0 {
				required, err = e.resolveReference(auth, target)
				if err != nil {
					return last, resultPlan, err
				}
			}
			last, err = analyze.DescribeRoutine(op.Context(), e.analysisHost(op), statement, authorityOf(auth), required, input, analyze.Expected{}, nil, d.SQL, d.Name(), sqltype.CollationRef{})
			if err != nil {
				if d.SQL.Standard {
					return last, resultPlan, err
				}
				return last, resultPlan, exec.SQLRoutineContext(err, d.Name(), false)
			}
			if d.SQL.Standard {
				action, freezeErr := view.FreezeRoutineAction(auth.Catalog, op.Session.SessionToken, statement, last)
				if freezeErr != nil {
					return last, resultPlan, freezeErr
				}
				actions = append(actions, action)
			}
		}
		if i == len(d.SQL.Statements)-1 && !d.SQL.Void() {
			resultPlan, err = validateSQLRoutineTerminal(statement, last, d.SQL.Result)
			if err != nil {
				return last, resultPlan, exec.SQLRoutineContext(err, d.Name(), false)
			}
		}
	}
	if len(d.SQL.Statements) == 0 && !d.SQL.Void() {
		return last, resultPlan, exec.SQLRoutineContext(sqlRoutineFinalError(d.SQL.Result), d.Name(), false)
	}
	if d.SQL.Standard {
		d.SQLBody = stored.NewRoutineProgram(actions)
	}
	return last, resultPlan, nil
}

func sqlRoutineDataCommand(statement parser.Statement) bool {
	switch statement.(type) {
	case *parser.Select, *parser.Insert, *parser.Update, *parser.Delete, *parser.Merge:
		return true
	}
	return false
}
func routineCommandTag(statement parser.Statement) string {
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
func sqlRoutineFinalError(result sqltype.Usage) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P13", Message: "return type mismatch in function declared to return " + result.Type.DiagnosticName(), Detail: "Function's final statement must be SELECT or INSERT/UPDATE/DELETE/MERGE RETURNING."}
}
func validateSQLRoutineTerminal(statement parser.Statement, b analyze.Binding, result sqltype.Usage) (sqltype.CoercionPlan, error) {
	if _, selecting := statement.(*parser.Select); selecting && b.Query != nil {
		return validateSQLRoutineResult(b.Query.Root.Schema(), result)
	}
	if b.Returning != nil {
		return validateSQLRoutineResult(b.Returning.Schema, result)
	}
	return sqltype.CoercionPlan{}, sqlRoutineFinalError(result)
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
