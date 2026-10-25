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
	if owner.Domain() != DomainOf(e) || owner.Epoch() != e.Catalog.State.Epoch() {
		return ids.ErrCatalogReplaced
	}
	e.IndexWaits.AdmitLocked(index, owner, kind)
	return nil
}

func (e *Engine) clearIndexBuildWait(index ids.ObjectAddress, owner ids.TxRef, kind index.BuildWaitKind) {
	e.Mu.Lock()
	e.IndexWaits.ClearLocked(index, owner, kind)
	e.Mu.Unlock()
}

func (s *StatementScope) waitIndexWriters(idx ids.ObjectAddress) error {
	e := s.Engine()
	owners, err := e.Locks.IndexWriters(s.LockTicket, s.LockedRef)
	if err != nil {
		return err
	}
	// The complete granted cohort is frozen before admission is observable.
	e.Mu.Lock()
	err = e.admitIndexBuildWaitLocked(idx, s.TxRef, index.WaitWriters)
	e.Mu.Unlock()
	if err != nil {
		return err
	}
	defer e.clearIndexBuildWait(idx, s.TxRef, index.WaitWriters)
	for _, owner := range owners {
		if err := s.WriteHost().WaitTransaction(owner, lock.CompletionVirtual); err != nil {
			return err
		}
	}
	return nil
}
