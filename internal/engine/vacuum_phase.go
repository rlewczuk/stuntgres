package engine

import (
	"stuntgres/internal/engine/ids"
)

func vacuumDatabasePreflightLocked(e *Engine, s *StatementScope) error {
	s.Op.Session.Mu.Lock()
	defer s.Op.Session.Mu.Unlock()
	if err := PublicationOperationLocked(e, s.Op); err != nil {
		return err
	}
	if s.TxRef.Epoch() != e.Catalog.State.Epoch() || s.LockTicket == nil || s.LockTicket.OwnershipEnded() {
		return ids.ErrCatalogReplaced
	}
	return s.Op.Context().Err()
}

func vacuumPhaseLocked(e *Engine, s *StatementScope, phase string, total, processed int64) {
	p, ok := e.VacuumProgress[s.TxRef]
	if !ok {
		return
	}
	p.Fact.Phase = phase
	p.Fact.IndexesTotal = total
	p.Fact.IndexesProcessed = processed
	e.VacuumProgress[s.TxRef] = p
}

func endVacuumProgress(e *Engine, ref ids.TxRef) {
	e.Mu.Lock()
	delete(e.VacuumProgress, ref)
	e.Mu.Unlock()
}
