package engine

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

// Historical ALTER worklists are separate from unconditional successor DML.
// The caller supplies only selected valid CHECKs and required valid NN jobs.
func (s *StatementScope) validateAddedConstraintRows(validator alterRowValidator, versions []storage.RowVersion, ends *storage.DeletionRoot, frontier ids.CommitSeq, writes []storage.RowVersion, deletes *visibility.PrivateDeletes) error {
	var replaced map[ids.RowID]bool
	for _, row := range writes {
		if row.Base != 0 {
			if replaced == nil {
				replaced = make(map[ids.RowID]bool)
			}
			replaced[row.Row] = true
		}
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: s.op.Context(), Host: hostOf(s), Input: s.input, Output: s.output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	scan := func(rows []storage.RowVersion, committed bool) error {
		for _, row := range rows {
			if err := s.op.Context().Err(); err != nil {
				return err
			}
			if committed && replaced[row.Row] || !visibility.LiveVersion(row, frontier, ends, deletes) {
				continue
			}
			if err := validator.Validate(row.Values, &evaluation); err != nil {
				return err
			}
		}
		return nil
	}
	if err := scan(versions, true); err != nil {
		return err
	}
	return scan(writes, false)
}
