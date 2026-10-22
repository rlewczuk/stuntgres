package engine

import (
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
)

// AdmitInsertRow admits one INSERT row: the unique check, the ON CONFLICT DO
// NOTHING arbitration, or the DO UPDATE action. It reports whether the row
// joins the batch.
func AdmitInsertRow(h dml.Host, ref ids.ObjectRef, candidate *index.WriteCandidate, prefix index.WriteBatch, evaluation *exec.Context) (bool, error) {
	action := h.Command().InsertConflict
	if action == nil {
		return true, checkInsertUniqueRow(h, ref, candidate, prefix)
	}
	if action.Update != nil {
		return false, admitInsertConflictUpdate(h, ref, candidate, evaluation)
	}
	return dml.AdmitInsertConflictRow(h, ref, candidate, prefix, index.ProbeRuntime)
}

// ApplyInsertBatch stages an unpartitioned INSERT batch and reports the
// affected row count; ON CONFLICT DO UPDATE has staged its rows already.
func ApplyInsertBatch(h dml.Host, ref ids.ObjectRef, rows index.WriteBatch) (int, error) {
	count := len(rows.Values)
	command := h.Command()
	if action := command.InsertConflict; action != nil && action.Update != nil {
		return action.Affected, nil
	}
	if action := command.InsertConflict; action != nil {
		action.Affected = count
	}
	err := dml.ApplyValidated(h, ref, rows)
	if action := command.InsertConflict; action != nil {
		count = action.Affected
	}
	if err != nil {
		return 0, err
	}
	return count, nil
}
