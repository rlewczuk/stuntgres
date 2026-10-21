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
		if err := s.writeHost().WaitTransaction(owner, lock.CompletionVirtual); err != nil {
			return err
		}
	}
	return nil
}
