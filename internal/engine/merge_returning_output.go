package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqltype"
)

func newMergeReturningOutput(b analyze.Binding, s *StatementScope) *mergeReturningOutput {
	if b.Returning == nil {
		return nil
	}
	rows := exec.NewFinalRows(b.Returning.Schema, 0)
	rows.Output = s.output
	return &mergeReturningOutput{Returning: b.Returning, Rows: rows, Frame: make([]sqltype.Value, b.Merge.TargetWidth+b.Merge.SourceWidth)}
}
