package engine

import "stuntgres/internal/sqltype"

func bindingForConflict(ref ObjectRef, parent *relationDescriptor, action *boundInsertConflict) binding {
	return binding{ref: ref, relation: parent, targets: action.targets, conflict: action}
}
func (s *StatementScope) admitInsertRow(ref ObjectRef, row []sqltype.Value, prefix [][]sqltype.Value) (bool, error) {
	action := s.insertConflict
	if action == nil {
		return true, s.checkInsertUniqueRow(ref, row, prefix)
	}
	manager := s.op.session.Engine().locks
	if action.statementKeys == nil {
		action.statementKeys = manager.checkpointUnique(s.ticket, nil)
	}
	action.checkpoint = manager.checkpointUnique(s.ticket, action.checkpoint)
	for {
		conflict, err := s.probeUniqueRow(ref, row, nil, prefix, action)
		if err != nil {
			return false, err
		}
		if conflict.index != nil {
			if err := s.insertConflictVisible(conflict.row, conflict.committed); err != nil {
				return false, err
			}
			manager.restoreUnique(s.ticket, action.checkpoint)
			s.discardExclusionCandidate(ref, row)
			return false, nil
		}
		other, err := s.probeUniqueRow(ref, row, nil, prefix, nil)
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

func (w *WriteStage) applyInsertValidated(ref ObjectRef, rows [][]sqltype.Value) (int, error) {
	count := len(rows)
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
func (s *StatementScope) revalidateConflictRows(ref ObjectRef, rows [][]sqltype.Value) ([][]sqltype.Value, error) {
	s.op.session.Engine().locks.restoreUnique(s.ticket, s.insertConflict.statementKeys)
	s.uniqueBinding = nil
	binding, err := s.bindUniqueWrite(ref)
	if err != nil {
		return nil, err
	}
	evaluation := EvalContext{Context: s.op.Context(), Input: s.input, Output: s.output}
	if err := s.insertConflict.selectArbiters(&evaluation, binding.parent, CatalogRead{owner: s.op.session.Engine(), state: binding.catalog}); err != nil {
		return nil, err
	}
	accepted := rows[:0]
	for _, row := range rows {
		admitted, err := s.admitInsertRow(ref, row, accepted)
		if err != nil {
			return nil, err
		}
		if admitted {
			accepted = append(accepted, row)
		}
	}
	clear(rows[len(accepted):])
	return accepted, nil
}
