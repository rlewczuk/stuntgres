package engine

// Capturing granted holders is not an acquisition of Share: later writers stay
// admissible and cannot be appended to this exact transaction cohort.
func (m *relationLockManager) indexWriters(ticket *relationTicket, parent ObjectRef) ([]*relationTicket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.valid(ticket) || parent.Epoch != ticket.ref.epoch {
		return nil, ErrCatalogReplaced
	}
	var cohort []*relationTicket
	if entry := m.entries[lockResource{ObjectRef: parent}]; entry != nil {
		for owner, mode := range entry.holders {
			if !sameLockSession(owner, ticket) && m.valid(owner) && lockConflict(relationShare, mode.mode) {
				cohort = append(cohort, owner)
			}
		}
	}
	return cohort, nil
}

type indexBuildWaitKind uint8

const (
	indexWaitWriters indexBuildWaitKind = iota + 1
	indexWaitOlderSnapshots
)

type indexBuildWait struct {
	owner TxRef
	kind  indexBuildWaitKind
}

// Engine.mu is held. This observes a frozen coordinator cohort, not registration
// of an individual lock-manager request.
func (e *Engine) admitIndexBuildWaitLocked(index ObjectAddress, owner TxRef, kind indexBuildWaitKind) error {
	if index == (ObjectAddress{}) {
		return nil
	}
	if owner.owner != e || owner.epoch != e.state.epoch {
		return ErrCatalogReplaced
	}
	if e.indexBuildWaits == nil {
		e.indexBuildWaits = make(map[ObjectAddress]indexBuildWait)
	}
	e.indexBuildWaits[index] = indexBuildWait{owner: owner, kind: kind}
	return nil
}

func (e *Engine) clearIndexBuildWait(index ObjectAddress, owner TxRef, kind indexBuildWaitKind) {
	e.mu.Lock()
	if e.indexBuildWaits[index] == (indexBuildWait{owner: owner, kind: kind}) {
		delete(e.indexBuildWaits, index)
	}
	e.mu.Unlock()
}

type completionLockPurpose uint8

const (
	completionVirtual completionLockPurpose = iota
	completionTransaction
)

func (s *StatementScope) waitCompletion(owner *relationTicket, purpose completionLockPurpose) error {
	e := s.op.session.Engine()
	policy := relationLockPolicy{operation: s.op, timeout: s.op.lockTimeout, statementDeadline: s.op.statementDeadline, source: s.op.sourceContext, completionPurpose: purpose}
	request, err := e.locks.startAcquire(s.op.Context(), s.ticket, lockResource{completion: owner}, transactionCompletion, policy)
	if request != nil {
		if s.replacementRead {
			e.replacementMu.RUnlock()
			s.replacementRead = false
		}
		err = request.wait()
	}
	return err
}

func (s *StatementScope) waitIndexWriters(index ObjectAddress) error {
	e := s.op.session.Engine()
	owners, err := e.locks.indexWriters(s.ticket, s.lockedRef)
	if err != nil {
		return err
	}
	// The complete granted cohort is frozen before admission is observable.
	e.mu.Lock()
	err = e.admitIndexBuildWaitLocked(index, s.txRef, indexWaitWriters)
	e.mu.Unlock()
	if err != nil {
		return err
	}
	defer e.clearIndexBuildWait(index, s.txRef, indexWaitWriters)
	for _, owner := range owners {
		if err := s.waitCompletion(owner, completionVirtual); err != nil {
			return err
		}
	}
	return nil
}
