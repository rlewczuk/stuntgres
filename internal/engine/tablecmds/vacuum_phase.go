package tablecmds

import (
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
)

func vacuumDatabasePreflightLocked(e *core.Engine, s *core.StatementScope) error {
	s.Op.Session.Mu.Lock()
	defer s.Op.Session.Mu.Unlock()
	if err := core.PublicationOperationLocked(e, s.Op); err != nil {
		return err
	}
	if s.TxRef.Epoch() != e.Catalog.State.Epoch() || s.LockTicket == nil || s.LockTicket.OwnershipEnded() {
		return ids.ErrCatalogReplaced
	}
	return s.Op.Context().Err()
}

func vacuumPhaseLocked(e *core.Engine, s *core.StatementScope, phase string, total, processed int64) {
	p, ok := e.VacuumProgress[s.TxRef]
	if !ok {
		return
	}
	p.Fact.Phase = phase
	p.Fact.IndexesTotal = total
	p.Fact.IndexesProcessed = processed
	e.VacuumProgress[s.TxRef] = p
}

func endVacuumProgress(e *core.Engine, ref ids.TxRef) {
	e.Mu.Lock()
	delete(e.VacuumProgress, ref)
	e.Mu.Unlock()
}
