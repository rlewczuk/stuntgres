package engine

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/visibility"
)

// Historical ALTER worklists are separate from unconditional successor DML.
// The caller supplies only selected valid CHECKs and required valid NN jobs.
func validateAddedConstraintRows(evaluation exec.Context, validator table.AlterRowValidator, versions []storage.RowVersion, ends *storage.DeletionRoot, frontier ids.CommitSeq, writes []storage.RowVersion, deletes *visibility.PrivateDeletes) error {
	var replaced map[ids.RowID]bool
	for _, row := range writes {
		if row.Base != 0 {
			if replaced == nil {
				replaced = make(map[ids.RowID]bool)
			}
			replaced[row.Row] = true
		}
	}
	scan := func(rows []storage.RowVersion, committed bool) error {
		for _, row := range rows {
			if err := evaluation.Context.Err(); err != nil {
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
