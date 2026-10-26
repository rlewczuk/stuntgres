package schemacmds

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// ExecuteSQLRoutineDDL runs CREATE FUNCTION, CREATE PROCEDURE and CREATE
// AGGREGATE through ExecuteSchema.
func ExecuteSQLRoutineDDL(e *core.Engine, scope *core.StatementScope, s parser.Statement, count int) (core.Result, error) {
	result, err := ExecuteSchema(e, scope, scope.Op, s, count, scope.Input)
	if err == nil {
		scope.CompleteUtility()
	}
	return result, err
}

// CREATE and Restore inspect the complete body without opening any producer.
// Runtime text binding uses only the reached command, never this validator.
func BindSQLRoutineLocked(e *core.Engine, op *session.Operation, auth core.AuthorizationView, d *catalog.Procedure, input sqltype.InputEnvironment, scope *core.StatementScope) (last analyze.Binding, resultPlan proc.ResultPlan, err error) {
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
				return last, resultPlan, core.SQLError("0A000", core.RoutineCommandTag(statement)+" is not yet supported in unquoted SQL function body")
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
				required, err = e.ResolveReference(auth, target)
				if err != nil {
					return last, resultPlan, err
				}
			}
			last, err = analyze.DescribeRoutine(op.Context(), e.AnalysisHost(op), statement, core.AuthorityOf(auth), required, input, analyze.Expected{}, nil, d.SQL, d.Name(), sqltype.CollationRef{})
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
			resultPlan, err = core.ValidateSQLRoutineTerminal(statement, last, d.SQL.Result, auth)
			if err != nil {
				return last, resultPlan, exec.SQLRoutineContext(err, d.Name(), false)
			}
		}
	}
	if len(d.SQL.Statements) == 0 && !d.SQL.Void() {
		name, err := core.CaptureTypeDiagnosticName(auth)
		if err != nil {
			return last, resultPlan, err
		}
		return last, resultPlan, exec.SQLRoutineContext(core.SQLRoutineFinalError(d.SQL.Result, name), d.Name(), false)
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
