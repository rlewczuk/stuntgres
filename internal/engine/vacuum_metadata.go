package engine

import (
	"context"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

type vacuumProgress struct {
	fact  pgcatalog.VacuumProgressFact
	owner authorization.PrincipalRef
}

func (e *Engine) vacuumCutoffLocked() (uint64, error) {
	cutoff, err := visibility.NextFullXID(e.visibility.LastAssignedXID())
	if err != nil {
		return 0, err
	}
	for _, entry := range e.visibility.Entries {
		if entry.FullXID != 0 && (entry.Ticket == nil || !entry.Ticket.OwnershipEnded()) && entry.FullXID < cutoff {
			cutoff = entry.FullXID
		}
	}
	return cutoff, nil
}
func (e *Engine) vacuumRetirementAllowedLocked(self ids.TxRef) bool {
	for ref, entry := range e.visibility.Entries {
		if ref == self || ref.Epoch() != self.Epoch() {
			continue
		}
		if entry.Snapshot || entry.SafeBuilder || entry.XID != 0 || entry.Ticket != nil && !entry.Ticket.OwnershipEnded() {
			return false
		}
	}
	return true
}
func (e *Engine) initializeMaintenanceLocked(store *storage.Relation) {
	if store == nil || store.Maintenance.FrozenXID != 0 {
		return
	}
	// A transaction can assign its canonical XID before this relation exists,
	// then publish into it later under READ COMMITTED. Empty relations must
	// therefore start no later than the oldest still-active assigned publisher.
	next, err := e.vacuumCutoffLocked()
	if err != nil {
		return
	}
	for _, row := range store.Versions {
		if row.Frozen {
			continue
		}
		if row.InsertFullXID == 0 {
			return
		}
		if row.InsertFullXID < next {
			next = row.InsertFullXID
		}
	}
	store.Maintenance.FrozenXID = next
}
func (e *Engine) updateDatabaseMaintenanceLocked() {
	// A still-active publisher can create or first write a relation after this
	// calculation; installed relation bounds alone cannot justify the seed.
	next, err := e.vacuumCutoffLocked()
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
	principals, err := r.auth.Roles.RoleClosure(ctx, r.auth.State.CurrentUser, true)
	if err != nil {
		return err
	}
	reader, hasReader := r.auth.Roles.Lookup("pg_read_all_stats")
	actor, hasActor := r.auth.Roles.Find(r.auth.State.CurrentUser)
	for _, p := range r.engine.vacuumProgress {
		fact := p.fact
		fact.Visible = hasActor && actor.Superuser
		for _, principal := range principals {
			if principal == p.owner || hasReader && principal == reader.Ref {
				fact.Visible = true
			}
		}
		if err := visit(fact); err != nil {
			return err
		}
	}
	return nil
}

// Database identity comes from the existing descriptor, not an allocator guess.
func vacuumDatabaseOID(view catalog.Read) ids.OID {
	database, ok := view.Database(catalog.CurrentDatabaseAddress)
	if !ok {
		return 0
	}
	return database.Address().ObjectID
}
