package engine

import (
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
)

func (s *StatementScope) admitInsertRow(ref ids.ObjectRef, candidate *index.WriteCandidate, prefix index.WriteBatch, evaluation *exec.Context) (bool, error) {
	action := s.InsertConflict
	if action == nil {
		return true, s.checkInsertUniqueRow(ref, candidate, prefix)
	}
	if action.Update != nil {
		return false, s.admitInsertConflictUpdate(ref, candidate, evaluation)
	}
	return dml.AdmitInsertConflictRow(s.writeHost(), ref, candidate, prefix, index.ProbeRuntime)
}

// uniqueJournal is the statement ticket's unique-key journal.
func (s *StatementScope) uniqueJournal() lock.UniqueJournal {
	return s.engine().locks.UniqueJournal(s.ticket)
}

func (w *WriteStage) applyInsertBatch(ref ids.ObjectRef, rows index.WriteBatch) (int, error) {
	count := len(rows.Values)
	if action := w.scope.InsertConflict; action != nil && action.Update != nil {
		return action.Affected, nil
	}
	if action := w.scope.InsertConflict; action != nil {
		action.Affected = count
	}
	err := dml.ApplyValidated(w.scope.writeHost(), ref, rows)
	if action := w.scope.InsertConflict; action != nil {
		count = action.Affected
	}
	if err != nil {
		return 0, err
	}
	return count, nil
}
