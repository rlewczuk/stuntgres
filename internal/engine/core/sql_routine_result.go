package core

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// sqlRoutineTerminalError carries the reached validator's phase, not a new
// validation result. Nested routine failures retain their own definition owner.
type sqlRoutineTerminalError struct {
	err        error
	definition *proc.Definition
	simple     bool
}

func (e *sqlRoutineTerminalError) Error() string { return e.err.Error() }
func (e *sqlRoutineTerminalError) Unwrap() error { return e.err }

// Terminal return checking follows the attempted inlining analysis, but only a
// simple scalar projection reaches that check while still in the inline path.
// Syntax guards preserve structure that analyzed plans may legitimately erase;
// analyzed project flags and typed expression walking reject real aggregate,
// window, set and subquery structure without source-name guesses or rebinding.
func sqlRoutineSimpleTerminal(statement parser.Statement, b analyze.Binding) bool {
	query, ok := statement.(*parser.Select)
	if !ok || len(query.Targets) != 1 || query.From != nil || query.Values != nil ||
		len(query.With) != 0 || query.WithRecursive || query.Where != nil ||
		query.GroupBy != nil || query.Having != nil || len(query.Windows) != 0 ||
		query.Distinct != nil || query.Order != nil || query.Into != nil ||
		len(query.Locking) != 0 || query.Bounds != nil || query.Set != nil {
		return false
	}
	if b.Query == nil {
		return false
	}
	project, ok := b.Query.Root.(*plan.ProjectNode)
	if !ok || len(project.Expressions) != 1 || project.HasTargetSet {
		return false
	}
	if _, singleton := project.Child.(*plan.SingletonNode); !singleton {
		return false
	}
	simple := true
	plan.WalkExpression(project.Expressions[0], func(node expr.Expr) {
		switch node.(type) {
		case plan.AggregateExpr, plan.GroupingExpr, *plan.WindowExpr, plan.SubqueryExpr:
			simple = false
		}
	})
	return simple
}

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

func ValidateSQLRoutineTerminal(statement parser.Statement, b analyze.Binding, result sqltype.Usage, auth AuthorizationView, definition *proc.Definition) (proc.ResultPlan, error) {
	// CREATE/Restore may supply the catalog's declaration-only RECORD.
	// Only that admitted identity becomes a private executable result usage;
	// shaped records and foreign same-OID descriptors retain their own checks.
	if result.Type != nil && result.Type.ID() == 2249 && !result.Type.IsComposite() && !result.Type.IsDeferredRecord() {
		if declared, ok := auth.Catalog.LookupType(2249); ok && declared == result.Type {
			result.Type = sqltype.DeferredRecord()
		}
	}
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
	adapt := func(columns []catalog.Column) (proc.ResultPlan, error) {
		if definition != nil && definition.Procedure {
			usages := make([]sqltype.Usage, len(columns))
			for i, column := range columns {
				usages[i] = column.Usage
			}
			return proc.PlanTuple(usages, result.Type)
		}
		return validateSQLRoutineResult(columns, result, display)
	}
	if _, selecting := statement.(*parser.Select); selecting && b.Query != nil {
		plan, err = adapt(b.Query.Root.Schema())
	} else if b.Returning != nil {
		plan, err = adapt(b.Returning.Schema)
	} else {
		err = SQLRoutineFinalError(result, display)
	}
	if namingErr != nil {
		return proc.ResultPlan{}, namingErr
	}
	return plan, err
}

func validateSQLRoutineResult(columns []catalog.Column, result sqltype.Usage, names ...func(sqltype.ID) string) (proc.ResultPlan, error) {
	members := make([]sqltype.CompositeMember, len(columns))
	for i, column := range columns {
		members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: column.Name, Usage: column.Usage}
	}
	return proc.PlanNamedResult(members, result, names...)
}
