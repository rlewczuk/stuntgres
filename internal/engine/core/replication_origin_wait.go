package core

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

// Subscription's internal origin removal waits, unlike explicit SQL origin
// drop's nowait error. The caller holds Engine.Mu and retains its DDL stack.
// Suspend its replacement reservation and mutex, then readmit the exact live
// operation/catalog/authorization before continuing. No peer journal is read.
func (e *Engine) waitOriginReleaseLocked(op *session.Operation, id ids.OID) error {
	if e.Cluster == nil || e.Cluster.replicationOriginOwners[id] == nil {
		return nil
	}
	before, err := e.AuthorizationLocked(op)
	if err != nil {
		return err
	}
	scope := ActiveScopeOfLease(op.Lease)
	store := op.EffectiveSettings()
	for owner := e.Cluster.replicationOriginOwners[id]; owner != nil; owner = e.Cluster.replicationOriginOwners[id] {
		if owner.released == nil {
			owner.released = make(chan struct{})
		}
		released := owner.released
		if scope != nil {
			scope.releaseCatalogReservation()
		}
		e.Mu.Unlock()
		func() {
			defer e.Mu.Lock()
			select {
			case <-op.Context().Done():
			case <-released:
			}
		}()
		store.Mu.Lock()
		op.Session.Mu.Lock()
		err = store.AdmitLocked(DomainOf(e), op)
		op.Session.Mu.Unlock()
		store.Mu.Unlock()
		if err != nil {
			return err
		}
		if ActiveScopeOfLease(op.Lease) != scope || scope != nil && scope.Op != op ||
			op.EffectiveSettings() != store || before.Catalog.Epoch() != e.Catalog.State.Epoch() {
			return ids.ErrCatalogReplaced
		}
		current, err := e.AuthorizationLocked(op)
		if err != nil {
			return err
		}
		if current.Catalog.State() != before.Catalog.State() || current.Catalog.Domain() != before.Catalog.Domain() ||
			current.Catalog.Epoch() != before.Catalog.Epoch() || current.Revision != before.Revision ||
			current.RoleRecord != before.RoleRecord || current.State != before.State {
			return ids.ErrCatalogReplaced
		}
	}
	return nil
}
