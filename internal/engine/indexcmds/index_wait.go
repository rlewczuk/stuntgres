package indexcmds

import (
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"

	"stuntgres/internal/engine/lock"
)

// Engine.mu is held. This observes a frozen coordinator cohort, not registration
// of an individual lock-manager request.
func admitIndexBuildWaitLocked(e *core.Engine, index ids.ObjectAddress, owner ids.TxRef, kind index.BuildWaitKind) error {
	if index == (ids.ObjectAddress{}) {
		return nil
	}
	if owner.Domain() != core.DomainOf(e) || owner.Epoch() != e.Catalog.State.Epoch() {
		return ids.ErrCatalogReplaced
	}
	e.IndexWaits.AdmitLocked(index, owner, kind)
	return nil
}

func clearIndexBuildWait(e *core.Engine, index ids.ObjectAddress, owner ids.TxRef, kind index.BuildWaitKind) {
	e.Mu.Lock()
	e.IndexWaits.ClearLocked(index, owner, kind)
	e.Mu.Unlock()
}

func waitIndexWriters(s *core.StatementScope, idx ids.ObjectAddress) error {
	e := s.Engine()
	owners, err := e.Locks.IndexWriters(s.LockTicket, s.LockedRef)
	if err != nil {
		return err
	}
	// The complete granted cohort is frozen before admission is observable.
	e.Mu.Lock()
	s.IndexProgressWait(len(owners), 0, 0)
	err = admitIndexBuildWaitLocked(e, idx, s.TxRef, index.WaitWriters)
	e.Mu.Unlock()
	if err != nil {
		return err
	}
	defer clearIndexBuildWait(e, idx, s.TxRef, index.WaitWriters)
	defer s.IndexProgressWait(0, 0, 0)
	for i, owner := range owners {
		pid, err := e.Locks.ProgressPID(owner)
		if err != nil {
			return err
		}
		s.IndexProgressWait(len(owners), i, pid)
		if err := s.WriteHost().WaitTransaction(owner, lock.CompletionVirtual); err != nil {
			return err
		}
		s.IndexProgressWait(len(owners), i+1, pid)
	}
	return nil
}
