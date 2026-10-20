package engine

import (
	"fmt"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Called only for admitted, finalized rows, after base constraints/uniqueness.
// A stack copy separates NEW from UPDATE FROM's OLD/source/RETURNING frame.
func (e *Engine) checkViewRow(scope *StatementScope, b *analyze.Binding, row []sqltype.Value, evaluation *exec.Context) error {
	if b.ViewWrite == nil || len(viewWriteOf(b.ViewWrite).checks) == 0 {
		return nil
	}
	newFrame := *evaluation
	newFrame.Row = row
	newFrame.Scope, newFrame.Frame = 1, 1
	for i := len(viewWriteOf(b.ViewWrite).checks) - 1; i >= 0; i-- {
		group := &viewWriteOf(b.ViewWrite).checks[i]
		for j := len(group.predicates) - 1; j >= 0; j-- {
			if err := exec.EvaluationContext(&newFrame); err != nil {
				return err
			}
			value, err := group.predicates[j].Eval(&newFrame)
			if err != nil {
				return err
			}
			truth, err := plan.TruthOf(value)
			if err != nil {
				return err
			}
			if truth != plan.True {
				detail, err := e.rowDiagnosticDetail(scope, b, row, true)
				if err != nil {
					return err
				}
				return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "44000", Message: fmt.Sprintf("new row violates check option for view \"%s\"", group.relation.Name()), Detail: detail}
			}
		}
	}
	return exec.EvaluationContext(&newFrame)
}
