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

// indexWaitRegistry holds the CONCURRENTLY build waits that InspectIndex
// reports, by index. It has no mutex: Engine.mu guards it, and every method
// requires the caller to hold Engine.mu.
type indexWaitRegistry struct {
	waits map[ids.ObjectAddress]indexBuildWait
}

// admitLocked records that owner's build of index waits for kind.
func (r *indexWaitRegistry) admitLocked(index ids.ObjectAddress, owner ids.TxRef, kind indexBuildWaitKind) {
	if r.waits == nil {
		r.waits = make(map[ids.ObjectAddress]indexBuildWait)
	}
	r.waits[index] = indexBuildWait{owner: owner, kind: kind}
}

// clearLocked drops the wait of index if it is still owner's wait for kind.
func (r *indexWaitRegistry) clearLocked(index ids.ObjectAddress, owner ids.TxRef, kind indexBuildWaitKind) {
	if r.waits[index] == (indexBuildWait{owner: owner, kind: kind}) {
		delete(r.waits, index)
	}
}

// endOwnerLocked drops every wait owner holds; its transaction ended.
func (r *indexWaitRegistry) endOwnerLocked(owner ids.TxRef) {
	for index, wait := range r.waits {
		if wait.owner == owner {
			delete(r.waits, index)
		}
	}
}

// clearAllLocked drops every wait; the serializable fence replaced the epoch.
func (r *indexWaitRegistry) clearAllLocked() { clear(r.waits) }

// lookupLocked returns the wait recorded for index.
func (r *indexWaitRegistry) lookupLocked(index ids.ObjectAddress) (indexBuildWait, bool) {
	wait, ok := r.waits[index]
	return wait, ok
}

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
