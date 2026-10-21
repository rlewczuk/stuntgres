package engine

import (
	"stuntgres/internal/engine/ids"

	"stuntgres/internal/engine/lock"
)

// Engine.mu is held. This observes a frozen coordinator cohort, not registration
// of an individual lock-manager request.
func (e *Engine) admitIndexBuildWaitLocked(index ids.ObjectAddress, owner ids.TxRef, kind indexBuildWaitKind) error {
	if index == (ids.ObjectAddress{}) {
		return nil
	}
	if owner.Domain() != domainOf(e) || owner.Epoch() != e.catalog.state.Epoch() {
		return ids.ErrCatalogReplaced
	}
	e.indexWaits.admitLocked(index, owner, kind)
	return nil
}

func (e *Engine) clearIndexBuildWait(index ids.ObjectAddress, owner ids.TxRef, kind indexBuildWaitKind) {
	e.mu.Lock()
	e.indexWaits.clearLocked(index, owner, kind)
	e.mu.Unlock()
}

func (s *StatementScope) waitCompletion(owner *lock.Ticket, purpose lock.CompletionPurpose) error {
	e := s.engine()
	policy := lock.Policy{TimeoutSlot: &s.op.LockWait, Timeout: s.op.LockTimeout, StatementDeadline: s.op.StatementDeadline, Source: s.op.SourceContext, CompletionPurpose: purpose}
	request, err := e.locks.StartAcquire(s.op.Context(), s.ticket, lock.CompletionOf(owner), lock.TransactionCompletion, policy)
	if request != nil {
		if s.replacementRead {
			e.replacementMu.RUnlock()
			s.replacementRead = false
		}
		err = request.Wait()
	}
	if err == nil && request != nil {
		return s.readmitProgramsAfterWait()
	}
	return err
}

func (s *StatementScope) waitIndexWriters(index ids.ObjectAddress) error {
	e := s.engine()
	owners, err := e.locks.IndexWriters(s.ticket, s.lockedRef)
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
		if err := s.waitCompletion(owner, lock.CompletionVirtual); err != nil {
			return err
		}
	}
	return nil
}
