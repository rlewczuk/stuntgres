package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
)

func bindingForConflict(ref ids.ObjectRef, parent *catalog.Relation, action *insertConflictState) analyze.Binding {
	return analyze.Binding{Ref: ref, Relation: parent, Targets: action.Targets, Insert: analyze.InsertBinding{Conflict: action.InsertConflict}, QuerySources: action.Sources, Returning: action.Returning}
}
func (s *StatementScope) admitInsertRow(ref ids.ObjectRef, candidate *index.WriteCandidate, prefix index.WriteBatch, evaluation *exec.Context) (bool, error) {
	action := s.insertConflict
	if action == nil {
		return true, s.checkInsertUniqueRow(ref, candidate, prefix)
	}
	if action.Update != nil {
		return false, s.admitInsertConflictUpdate(ref, candidate, evaluation)
	}
	return s.admitInsertConflictRow(ref, candidate, prefix, index.ProbeRuntime)
}

func (s *StatementScope) admitInsertConflictRow(ref ids.ObjectRef, candidate *index.WriteCandidate, prefix index.WriteBatch, purpose index.ProbePurpose) (bool, error) {
	action := s.insertConflict
	manager := s.engine().locks
	if action.statementKeys == nil {
		action.statementKeys = manager.CheckpointUnique(s.ticket, nil)
	}
	action.checkpoint = manager.CheckpointUnique(s.ticket, action.checkpoint)
	for {
		conflict, err := s.probeUniqueRow(ref, candidate, nil, prefix, action, purpose)
		if err != nil {
			return false, err
		}
		if conflict.index != nil {
			if err := s.insertConflictVisible(conflict.row, conflict.committed); err != nil {
				return false, err
			}
			manager.RestoreUnique(s.ticket, action.checkpoint)
			s.discardExclusionCandidate(ref, candidate)
			return false, nil
		}
		other, err := s.probeUniqueRow(ref, candidate, nil, prefix, nil, purpose)
		if err != nil {
			return false, err
		}
		// A provider publication during the unselected probe invalidates the first
		// pass; recapture selected arbiters before treating any hit as a violation.
		if s.uniqueBinding != nil && action.catalog != s.uniqueBinding.Catalog {
			continue
		}
		if other.index != nil {
			return false, other.diagnostic(s, ref)
		}
		return true, nil
	}
}
func (s *StatementScope) insertConflictVisible(row storage.RowVersion, committed bool) error {
	if committed && stableIsolation(s.owner.Isolation) && row.Born > s.owner.Frontier {
		return sqlError("40001", "could not serialize access due to concurrent update")
	}
	return nil
}

func (w *WriteStage) applyInsertBatch(ref ids.ObjectRef, rows index.WriteBatch) (int, error) {
	count := len(rows.Values)
	if action := w.scope.insertConflict; action != nil && action.Update != nil {
		return action.affected, nil
	}
	if action := w.scope.insertConflict; action != nil {
		action.affected = count
	}
	err := w.applyValidated(ref, rows)
	if action := w.scope.insertConflict; action != nil {
		count = action.affected
	}
	if err != nil {
		return 0, err
	}
	return count, nil
}
func (s *StatementScope) revalidateConflictRows(ref ids.ObjectRef, rows index.WriteBatch) (index.WriteBatch, error) {
	prior := s.uniqueBinding
	s.engine().locks.RestoreUnique(s.ticket, s.insertConflict.statementKeys)
	s.uniqueBinding = nil
	binding, err := s.bindUniqueWrite(ref)
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
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: s.op.Context(), Input: s.input, Output: s.output}}
	if err := s.insertConflict.selectArbiters(&evaluation, binding.Parent, s.engine().catalog.read(binding.Catalog)); err != nil {
		return index.WriteBatch{}, err
	}
	accepted := rows.Prefix(0)
	for i := range rows.Values {
		row := rows.Candidate(i)
		admitted, err := s.admitInsertConflictRow(ref, &row, accepted, purpose)
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
