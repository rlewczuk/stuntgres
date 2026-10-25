package engine

import (
	"context"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/table"
)

type VacuumProgress struct {
	Fact  pgcatalog.VacuumProgressFact
	Owner authorization.PrincipalRef
}

func (e *Engine) UpdateDatabaseMaintenanceLocked() {
	// A still-active publisher can create or first write a relation after this
	// calculation; installed relation bounds alone cannot justify the seed.
	cutoff, err := e.Visibility.VacuumCutoffLocked()
	if err != nil {
		return
	}
	next, ok := table.DatabaseFrozenXIDLocked(e.Catalog.State, e.Storage.Relations, cutoff)
	if !ok {
		return
	}
	e.DatabaseFrozenXID = next
	e.publishDatabaseFrozenXIDLocked()
}
func (e *Engine) vacuumDatabasePreflightLocked(s *StatementScope) error {
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
func (e *Engine) vacuumPhaseLocked(s *StatementScope, phase string, total, processed int64) {
	p, ok := e.VacuumProgress[s.TxRef]
	if !ok {
		return
	}
	p.Fact.Phase = phase
	p.Fact.IndexesTotal = total
	p.Fact.IndexesProcessed = processed
	e.VacuumProgress[s.TxRef] = p
}
func (e *Engine) endVacuumProgress(ref ids.TxRef) {
	e.Mu.Lock()
	delete(e.VacuumProgress, ref)
	e.Mu.Unlock()
}
func (r EngineCatalogRuntime) Maintenance(address ids.ObjectAddress) (pgcatalog.MaintenanceFact, bool) {
	store := r.engine.Storage.Relations[address]
	if store == nil {
		return pgcatalog.MaintenanceFact{}, false
	}
	return pgcatalog.MaintenanceFact(store.Maintenance), true
}
func (r EngineCatalogRuntime) DatabaseFrozenXID() uint64 { return r.engine.DatabaseFrozenXID }
func (r EngineCatalogRuntime) VisitVacuumProgress(ctx context.Context, visit func(pgcatalog.VacuumProgressFact) error) error {
	if len(r.engine.VacuumProgress) == 0 {
		return nil
	}
	viewer, err := pgcatalog.NewVacuumProgressViewer(ctx, r.Auth.Roles, r.Auth.State.CurrentUser)
	if err != nil {
		return err
	}
	for _, p := range r.engine.VacuumProgress {
		fact := p.Fact
		fact.Visible = viewer.Visible(p.Owner)
		if err := visit(fact); err != nil {
			return err
		}
	}
	return nil
}
