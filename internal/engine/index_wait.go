package engine

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"

	"stuntgres/internal/engine/lock"
)

// Engine.mu is held. This observes a frozen coordinator cohort, not registration
// of an individual lock-manager request.
func (e *Engine) admitIndexBuildWaitLocked(index ids.ObjectAddress, owner ids.TxRef, kind index.BuildWaitKind) error {
	if index == (ids.ObjectAddress{}) {
		return nil
	}
	if owner.Domain() != domainOf(e) || owner.Epoch() != e.catalog.state.Epoch() {
		return ids.ErrCatalogReplaced
	}
	e.indexWaits.AdmitLocked(index, owner, kind)
	return nil
}

func (e *Engine) clearIndexBuildWait(index ids.ObjectAddress, owner ids.TxRef, kind index.BuildWaitKind) {
	e.mu.Lock()
	e.indexWaits.ClearLocked(index, owner, kind)
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

func (s *StatementScope) waitIndexWriters(idx ids.ObjectAddress) error {
	e := s.engine()
	owners, err := e.locks.IndexWriters(s.ticket, s.lockedRef)
	if err != nil {
		return err
	}
	// The complete granted cohort is frozen before admission is observable.
	e.mu.Lock()
	err = e.admitIndexBuildWaitLocked(idx, s.txRef, index.WaitWriters)
	e.mu.Unlock()
	if err != nil {
		return err
	}
	defer e.clearIndexBuildWait(idx, s.txRef, index.WaitWriters)
	for _, owner := range owners {
		if err := s.waitCompletion(owner, lock.CompletionVirtual); err != nil {
			return err
		}
	}
	return nil
}
