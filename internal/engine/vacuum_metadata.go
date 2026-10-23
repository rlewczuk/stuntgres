package engine

import (
	"context"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
)

type vacuumProgress struct {
	fact  pgcatalog.VacuumProgressFact
	owner authorization.PrincipalRef
}

func (e *Engine) updateDatabaseMaintenanceLocked() {
	// A still-active publisher can create or first write a relation after this
	// calculation; installed relation bounds alone cannot justify the seed.
	next, err := e.visibility.VacuumCutoffLocked()
	if err != nil {
		return
	}
	for address, store := range e.storage.Relations {
		d, ok := e.catalog.state.Relation(address)
		if !ok || !vacuumHeap(d) {
			continue
		}
		bound := store.Maintenance.FrozenXID
		if bound == 0 {
			return
		}
		if bound < next {
			next = bound
		}
	}
	e.databaseFrozenXID = next
	e.publishDatabaseFrozenXIDLocked()
}
func (e *Engine) vacuumDatabasePreflightLocked(s *StatementScope) error {
	s.op.Session.Mu.Lock()
	defer s.op.Session.Mu.Unlock()
	if err := publicationOperationLocked(e, s.op); err != nil {
		return err
	}
	if s.txRef.Epoch() != e.catalog.state.Epoch() || s.ticket == nil || s.ticket.OwnershipEnded() {
		return ids.ErrCatalogReplaced
	}
	return s.op.Context().Err()
}
func (e *Engine) vacuumPhaseLocked(s *StatementScope, phase string, total, processed int64) {
	p, ok := e.vacuumProgress[s.txRef]
	if !ok {
		return
	}
	p.fact.Phase = phase
	p.fact.IndexesTotal = total
	p.fact.IndexesProcessed = processed
	e.vacuumProgress[s.txRef] = p
}
func (e *Engine) endVacuumProgress(ref ids.TxRef) {
	e.mu.Lock()
	delete(e.vacuumProgress, ref)
	e.mu.Unlock()
}
func (r engineCatalogRuntime) Maintenance(address ids.ObjectAddress) (pgcatalog.MaintenanceFact, bool) {
	store := r.engine.storage.Relations[address]
	if store == nil {
		return pgcatalog.MaintenanceFact{}, false
	}
	return pgcatalog.MaintenanceFact(store.Maintenance), true
}
func (r engineCatalogRuntime) DatabaseFrozenXID() uint64 { return r.engine.databaseFrozenXID }
func (r engineCatalogRuntime) VisitVacuumProgress(ctx context.Context, visit func(pgcatalog.VacuumProgressFact) error) error {
	if len(r.engine.vacuumProgress) == 0 {
		return nil
	}
	viewer, err := pgcatalog.NewVacuumProgressViewer(ctx, r.auth.Roles, r.auth.State.CurrentUser)
	if err != nil {
		return err
	}
	for _, p := range r.engine.vacuumProgress {
		fact := p.fact
		fact.Visible = viewer.Visible(p.owner)
		if err := visit(fact); err != nil {
			return err
		}
	}
	return nil
}

// Database identity comes from the existing descriptor, not an allocator guess.
func vacuumDatabaseOID(view catalog.Read) ids.OID {
	database, ok := view.Database(view.Owner().DatabaseAddress())
	if !ok {
		return 0
	}
	return database.Address().ObjectID
}
