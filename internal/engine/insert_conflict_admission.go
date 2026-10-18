package engine

import "stuntgres/internal/engine/ids"

func bindingForConflict(ref ids.ObjectRef, parent *relationDescriptor, action *boundInsertConflict) binding {
	return binding{ref: ref, relation: parent, targets: action.targets, conflict: action, querySources: action.sources, returning: action.returning}
}
func (s *StatementScope) admitInsertRow(ref ids.ObjectRef, candidate *indexWriteCandidate, prefix indexWriteBatch, evaluation *EvalContext) (bool, error) {
	action := s.insertConflict
	if action == nil {
		return true, s.checkInsertUniqueRow(ref, candidate, prefix)
	}
	if action.update != nil {
		return false, s.admitInsertConflictUpdate(ref, candidate, evaluation)
	}
	return s.admitInsertConflictRow(ref, candidate, prefix, indexProbeRuntime)
}

func (s *StatementScope) admitInsertConflictRow(ref ids.ObjectRef, candidate *indexWriteCandidate, prefix indexWriteBatch, purpose indexProbePurpose) (bool, error) {
	action := s.insertConflict
	manager := s.op.session.Engine().locks
	if action.statementKeys == nil {
		action.statementKeys = manager.checkpointUnique(s.ticket, nil)
	}
	action.checkpoint = manager.checkpointUnique(s.ticket, action.checkpoint)
	for {
		conflict, err := s.probeUniqueRow(ref, candidate, nil, prefix, action, purpose)
		if err != nil {
			return false, err
		}
		if conflict.index != nil {
			if err := s.insertConflictVisible(conflict.row, conflict.committed); err != nil {
				return false, err
			}
			manager.restoreUnique(s.ticket, action.checkpoint)
			s.discardExclusionCandidate(ref, candidate)
			return false, nil
		}
		other, err := s.probeUniqueRow(ref, candidate, nil, prefix, nil, purpose)
		if err != nil {
			return false, err
		}
		// A provider publication during the unselected probe invalidates the first
		// pass; recapture selected arbiters before treating any hit as a violation.
		if s.uniqueBinding != nil && action.catalog != s.uniqueBinding.catalog {
			continue
		}
		if other.index != nil {
			return false, other.diagnostic(s, ref)
		}
		return true, nil
	}
}
func (s *StatementScope) insertConflictVisible(row rowVersion, committed bool) error {
	if committed && stableIsolation(s.owner.isolation) && row.born > s.owner.frontier {
		return sqlError("40001", "could not serialize access due to concurrent update")
	}
	return nil
}

func (w *WriteStage) applyInsertValidated(ref ids.ObjectRef, rows indexWriteBatch) (int, error) {
	count := len(rows.values)
	if action := w.scope.insertConflict; action != nil && action.update != nil {
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
func (s *StatementScope) revalidateConflictRows(ref ids.ObjectRef, rows indexWriteBatch) (indexWriteBatch, error) {
	prior := s.uniqueBinding
	s.op.session.Engine().locks.restoreUnique(s.ticket, s.insertConflict.statementKeys)
	s.uniqueBinding = nil
	binding, err := s.bindUniqueWrite(ref)
	if err != nil {
		return indexWriteBatch{}, err
	}
	purpose := indexProbeRuntime
	if binding.checked || prior != nil && prior.checked {
		purpose = indexProbeRevalidation
		if prior != nil && !sameRelationRowShape(prior.parent, binding.parent) {
			return indexWriteBatch{}, ErrCatalogReplaced
		}
	}
	evaluation := EvalContext{Context: s.op.Context(), Input: s.input, Output: s.output}
	if err := s.insertConflict.selectArbiters(&evaluation, binding.parent, CatalogRead{owner: s.op.session.Engine(), state: binding.catalog}); err != nil {
		return indexWriteBatch{}, err
	}
	accepted := rows.prefix(0)
	for i := range rows.values {
		row := rows.candidate(i)
		admitted, err := s.admitInsertConflictRow(ref, &row, accepted, purpose)
		if err != nil {
			return indexWriteBatch{}, err
		}
		if admitted {
			accepted.append(row)
		}
	}
	clear(rows.values[len(accepted.values):])
	if rows.states != nil {
		clear(rows.states[len(accepted.values):])
	}
	return accepted, nil
}
