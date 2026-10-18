package engine

import (
	"fmt"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Called only for admitted, finalized rows, after base constraints/uniqueness.
// A stack copy separates NEW from UPDATE FROM's OLD/source/RETURNING frame.
func (e *Engine) checkViewRow(scope *StatementScope, b *binding, row []sqltype.Value, evaluation *EvalContext) error {
	if b.viewWrite == nil || len(b.viewWrite.checks) == 0 {
		return nil
	}
	newFrame := *evaluation
	newFrame.Row = row
	newFrame.Scope, newFrame.Frame = 1, 1
	for i := len(b.viewWrite.checks) - 1; i >= 0; i-- {
		group := &b.viewWrite.checks[i]
		for j := len(group.predicates) - 1; j >= 0; j-- {
			if err := evaluationContext(&newFrame); err != nil {
				return err
			}
			value, err := group.predicates[j].Eval(&newFrame)
			if err != nil {
				return err
			}
			truth, err := TruthOf(value)
			if err != nil {
				return err
			}
			if truth != True {
				detail, err := e.rowDiagnosticDetail(scope, b, row, true)
				if err != nil {
					return err
				}
				return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "44000", Message: fmt.Sprintf("new row violates check option for view \"%s\"", group.relation.Name()), Detail: detail}
			}
		}
	}
	return evaluationContext(&newFrame)
}
