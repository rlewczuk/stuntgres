package engine

import (
	"errors"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindMergeAction(raw parser.MergeActionExpr) (expr.Expr, error) {
	frame := uint32(0)
	if b.target.returningAliases != nil && b.target.returningAliases.mode == returningMerge {
		frame = b.frameID()
	} else {
		for i := len(b.query.scopes) - 1; i >= 0; i-- {
			s := &b.query.scopes[i]
			if s.returning != nil && s.returning.mode == returningMerge {
				frame = s.frame
				break
			}
		}
	}
	if frame == 0 {
		return nil, expr.ErrorAt("42601", "MERGE_ACTION() can only be used in the RETURNING list of a MERGE command", raw.Pos)
	}
	return plan.MergeActionExpr{Info: b.info(b.typ(sqltype.Text).Usage(), raw.Pos, expr.Properties{RowDependent: true}), Scope: 1, Frame: frame}, nil
}
func (b *expressionBinder) mergeReturningFrame(frame uint32) bool {
	if frame == b.frameID() && b.target.returningAliases != nil && b.target.returningAliases.mode == returningMerge {
		return true
	}
	for i := len(b.query.scopes) - 1; i >= 0; i-- {
		s := &b.query.scopes[i]
		if s.frame == frame && s.returning != nil && s.returning.mode == returningMerge {
			return true
		}
	}
	return false
}

// This execution-private collector never escapes before executeMerge's staging
// and the request's sole Finish. It reuses one full target-first/source frame.
type mergeReturningOutput struct {
	returning *boundReturning
	rows      *exec.FinalRows
	frame     []sqltype.Value
}

func newMergeReturningOutput(b binding, s *StatementScope) *mergeReturningOutput {
	if b.returning == nil {
		return nil
	}
	rows := exec.NewFinalRows(b.returning.schema, 0)
	rows.Output = s.output
	return &mergeReturningOutput{returning: b.returning, rows: rows, frame: make([]sqltype.Value, b.merge.targetWidth+b.merge.sourceWidth)}
}
func (o *mergeReturningOutput) project(b binding, c *exec.Context, action parser.MergeAction, old, row, source []sqltype.Value, sourcePresent bool) error {
	if o == nil {
		return nil
	}
	evaluation := *c
	evaluation.Scope, evaluation.Frame = 1, 1
	evaluation.MergeReturning, evaluation.MergeAction, evaluation.MergeSourcePresent = true, action, sourcePresent
	evaluation.Row, evaluation.OldRow = o.frame, old
	evaluation.PhysicalTable = b.ref
	if action == parser.MergeDeleteAction {
		row = old
	}
	copy(o.frame, row)
	copy(o.frame[b.merge.targetWidth:], source)
	for i, expr := range o.returning.expressions {
		value, err := expr.Eval(&evaluation)
		if err != nil {
			return err
		}
		if value.Type() == nil || !value.Type().Concrete() || value.Type() != o.rows.Columns[i].Usage.Type {
			return errors.New("stuntgres: invalid finalized MERGE value")
		}
		o.rows.Values = append(o.rows.Values, value)
	}
	o.rows.Count++
	return nil
}
