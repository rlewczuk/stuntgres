package engine

import (
	"errors"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// This execution-private collector never escapes before executeMerge's staging
// and the request's sole Finish. It reuses one full target-first/source frame.
type mergeReturningOutput struct {
	returning *boundReturning
	rows      *exec.FinalRows
	frame     []sqltype.Value
}

func newMergeReturningOutput(b binding, s *StatementScope) *mergeReturningOutput {
	if b.Returning == nil {
		return nil
	}
	rows := exec.NewFinalRows(b.Returning.Schema, 0)
	rows.Output = s.output
	return &mergeReturningOutput{returning: b.Returning, rows: rows, frame: make([]sqltype.Value, b.Merge.TargetWidth+b.Merge.SourceWidth)}
}

func (o *mergeReturningOutput) project(b binding, c *exec.Context, action parser.MergeAction, old, row, source []sqltype.Value, sourcePresent bool) error {
	if o == nil {
		return nil
	}
	evaluation := *c
	evaluation.Scope, evaluation.Frame = 1, 1
	evaluation.MergeReturning, evaluation.MergeAction, evaluation.MergeSourcePresent = true, action, sourcePresent
	evaluation.Row, evaluation.OldRow = o.frame, old
	evaluation.PhysicalTable = b.Ref
	if action == parser.MergeDeleteAction {
		row = old
	}
	copy(o.frame, row)
	copy(o.frame[b.Merge.TargetWidth:], source)
	for i, expr := range o.returning.Expressions {
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
