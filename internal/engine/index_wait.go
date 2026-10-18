package engine

import (
	"stuntgres/internal/engine/ids"

	"stuntgres/internal/engine/lock"
)

type indexBuildWaitKind uint8

const (
	indexWaitWriters indexBuildWaitKind = iota + 1
	indexWaitOlderSnapshots
)

type indexBuildWait struct {
	owner ids.TxRef
	kind  indexBuildWaitKind
}

// Engine.mu is held. This observes a frozen coordinator cohort, not registration
// of an individual lock-manager request.
func (e *Engine) admitIndexBuildWaitLocked(index ids.ObjectAddress, owner ids.TxRef, kind indexBuildWaitKind) error {
	if index == (ids.ObjectAddress{}) {
		return nil
	}
	if owner.Domain() != domainOf(e) || owner.Epoch() != e.catalog.state.epoch {
		return ErrCatalogReplaced
	}
	if e.indexBuildWaits == nil {
		e.indexBuildWaits = make(map[ids.ObjectAddress]indexBuildWait)
	}
	e.indexBuildWaits[index] = indexBuildWait{owner: owner, kind: kind}
	return nil
}

func (e *Engine) clearIndexBuildWait(index ids.ObjectAddress, owner ids.TxRef, kind indexBuildWaitKind) {
	e.mu.Lock()
	if e.indexBuildWaits[index] == (indexBuildWait{owner: owner, kind: kind}) {
		delete(e.indexBuildWaits, index)
	}
	e.mu.Unlock()
}

func (s *StatementScope) waitCompletion(owner *lock.Ticket, purpose lock.CompletionPurpose) error {
	e := s.op.session.Engine()
	policy := lock.Policy{TimeoutSlot: &s.op.lockWait, Timeout: s.op.lockTimeout, StatementDeadline: s.op.statementDeadline, Source: s.op.sourceContext, CompletionPurpose: purpose}
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
	e := s.op.session.Engine()
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
