package engine

import (
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
)

func (s *StatementScope) admitInsertRow(ref ids.ObjectRef, candidate *index.WriteCandidate, prefix index.WriteBatch, evaluation *exec.Context) (bool, error) {
	action := s.InsertConflict
	if action == nil {
		return true, s.checkInsertUniqueRow(ref, candidate, prefix)
	}
	if action.Update != nil {
		return false, s.admitInsertConflictUpdate(ref, candidate, evaluation)
	}
	return admitInsertConflictRow(s.writeHost(), ref, candidate, prefix, index.ProbeRuntime)
}

// uniqueJournal is the statement ticket's unique-key journal.
func (s *StatementScope) uniqueJournal() lock.UniqueJournal {
	return s.engine().locks.UniqueJournal(s.ticket)
}

func admitInsertConflictRow(h dml.Host, ref ids.ObjectRef, candidate *index.WriteCandidate, prefix index.WriteBatch, purpose index.ProbePurpose) (bool, error) {
	c := h.Command()
	action := c.InsertConflict
	journal := h.UniqueJournal()
	if action.StatementKeys == nil {
		action.StatementKeys = journal.Checkpoint(nil)
	}
	action.Checkpoint = journal.Checkpoint(action.Checkpoint)
	for {
		conflict, err := probeUniqueRow(h, ref, candidate, nil, prefix, action, purpose)
		if err != nil {
			return false, err
		}
		if conflict.index != nil {
			if err := insertConflictVisible(h, conflict.row, conflict.committed); err != nil {
				return false, err
			}
			journal.Restore(action.Checkpoint)
			h.DiscardExclusion(candidate)
			return false, nil
		}
		other, err := probeUniqueRow(h, ref, candidate, nil, prefix, nil, purpose)
		if err != nil {
			return false, err
		}
		// A provider publication during the unselected probe invalidates the first
		// pass; recapture selected arbiters before treating any hit as a violation.
		if c.UniqueBinding != nil && action.Catalog != c.UniqueBinding.Catalog {
			continue
		}
		if other.index != nil {
			return false, other.diagnostic(h, ref)
		}
		return true, nil
	}
}
func insertConflictVisible(h dml.Host, row storage.RowVersion, committed bool) error {
	if !committed {
		return nil
	}
	if stable, frontier := h.Snapshot(); stable && row.Born > frontier {
		return sqlError("40001", "could not serialize access due to concurrent update")
	}
	return nil
}

func (w *WriteStage) applyInsertBatch(ref ids.ObjectRef, rows index.WriteBatch) (int, error) {
	count := len(rows.Values)
	if action := w.scope.InsertConflict; action != nil && action.Update != nil {
		return action.Affected, nil
	}
	if action := w.scope.InsertConflict; action != nil {
		action.Affected = count
	}
	err := applyValidated(w.scope.writeHost(), ref, rows)
	if action := w.scope.InsertConflict; action != nil {
		count = action.Affected
	}
	if err != nil {
		return 0, err
	}
	return count, nil
}
func revalidateConflictRows(h dml.Host, ref ids.ObjectRef, rows index.WriteBatch) (index.WriteBatch, error) {
	c := h.Command()
	prior := c.UniqueBinding
	h.UniqueJournal().Restore(c.InsertConflict.StatementKeys)
	c.UniqueBinding = nil
	binding, err := bindUniqueWrite(h, ref)
	if err != nil {
		return index.WriteBatch{}, err
	}
	purpose := index.ProbeRuntime
	if binding.Checked || prior != nil && prior.Checked {
		purpose = index.ProbeRevalidation
		if prior != nil && !index.SameRelationRowShape(prior.Parent, binding.Parent) {
			return index.WriteBatch{}, ids.ErrCatalogReplaced
		}
	}
	input, output := h.Environments()
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: h.Context(), Input: input, Output: output}}
	if err := c.InsertConflict.SelectArbiters(&evaluation, binding.Parent, h.CatalogRead(binding.Catalog)); err != nil {
		return index.WriteBatch{}, err
	}
	accepted := rows.Prefix(0)
	for i := range rows.Values {
		row := rows.Candidate(i)
		admitted, err := admitInsertConflictRow(h, ref, &row, accepted, purpose)
		if err != nil {
			return index.WriteBatch{}, err
		}
		if admitted {
			accepted.Append(row)
		}
	}
	clear(rows.Values[len(accepted.Values):])
	if rows.States != nil {
		clear(rows.States[len(accepted.Values):])
	}
	return accepted, nil
}
