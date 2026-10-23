package engine

import (
	"context"
	"errors"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

// Admission serializes this incarnation. Register with the manager first, then
// associate its exact ticket before any command can reach canonical assignment.
// This function holds neither manager.mu nor TX synchronization while taking
// Engine.mu; the general order is documented on lock.Manager.
func (e *Engine) registerTransactionLocks(ref ids.TxRef) (*lock.Ticket, error) {
	if !session.TxOwnerAttached(ref) {
		return nil, errors.New("stuntgres: invalid lock owner")
	}
	ticket, err := e.locks.Register(ref)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	if ref.Epoch() != e.catalog.state.Epoch() {
		e.mu.Unlock()
		e.endLockOwner(ticket)
		return nil, ids.ErrCatalogReplaced
	}
	e.visibility.AttachTicketLocked(ref, ticket)
	e.mu.Unlock()
	return ticket, nil
}

// visibilityWriteLocked fences ref against this engine's current catalog
// epoch before the registry orders it as a writer.
func (e *Engine) visibilityWriteLocked(ref ids.TxRef) error {
	if ref.Domain() != domainOf(e) || ref.Epoch() != e.catalog.state.Epoch() {
		return ids.ErrCatalogReplaced
	}
	return e.visibility.WriteLocked(ref)
}

// visibilitySnapshotLocked fences ref against this engine's current catalog
// epoch before the registry advertises its snapshot.
func (e *Engine) visibilitySnapshotLocked(ref ids.TxRef, safeBuilder bool) (uint64, error) {
	if ref.Domain() != domainOf(e) || ref.Epoch() != e.catalog.state.Epoch() {
		return 0, ids.ErrCatalogReplaced
	}
	return e.visibility.SnapshotLocked(ref, safeBuilder)
}

func (e *Engine) endStatementVisibility(ref ids.TxRef, stable bool) {
	if stable {
		return
	}
	e.mu.Lock()
	e.visibility.DropSnapshotLocked(ref)
	e.mu.Unlock()
}

func (s *StatementScope) waitOlderIndexSnapshots(xmin uint64, idx ids.ObjectAddress) error {
	e := s.engine()
	e.mu.Lock()
	if s.txRef.Epoch() != e.catalog.state.Epoch() {
		e.mu.Unlock()
		return ids.ErrCatalogReplaced
	}
	// Drop every advertised builder phase before collecting the real wait cohort.
	e.visibility.ResetPhaseLocked(s.txRef)
	refs := e.visibility.OlderSnapshotsLocked(s.txRef, xmin)
	// Publish only after dropping validation ownership and freezing the cohort,
	// under the same critical section used by the sole coordinator observation.
	if err := e.admitIndexBuildWaitLocked(idx, s.txRef, index.WaitOlderSnapshots); err != nil {
		e.mu.Unlock()
		return err
	}
	e.mu.Unlock()
	defer e.clearIndexBuildWait(idx, s.txRef, index.WaitOlderSnapshots)
	for _, ref := range refs {
		e.mu.Lock()
		eligible := e.visibility.SnapshotOlderLocked(ref, xmin)
		e.mu.Unlock()
		if !eligible {
			continue
		}
		if owner := e.locks.TransactionTicket(ref); owner != nil {
			if err := s.writeHost().WaitTransaction(owner, lock.CompletionVirtual); err != nil {
				return err
			}
		}
	}
	return nil
}

// assignTransactionXIDLocked fences ref against the current catalog epoch.
func (e *Engine) assignTransactionXIDLocked(ref ids.TxRef) error {
	if ref.Domain() != domainOf(e) || ref.Epoch() != e.catalog.state.Epoch() {
		return ids.ErrCatalogReplaced
	}
	return e.visibility.AssignTransactionXIDLocked(ref)
}

// transactionAgeLocked fences ref against the current catalog epoch.
func (e *Engine) transactionAgeLocked(ref ids.TxRef, xid uint32) (int32, error) {
	if ref.Domain() != domainOf(e) || ref.Epoch() != e.catalog.state.Epoch() {
		return 0, ids.ErrCatalogReplaced
	}
	return e.visibility.TransactionAgeLocked(ref, xid)
}

// pinUniqueKey hashes the claimed values, then pins their interned lock
// resource. Hashing and comparison run outside every metadata mutex.
func (e *Engine) pinUniqueKey(ctx context.Context, ticket *lock.Ticket, ref ids.ObjectRef, ordering index.Ordering, grouping []sqltype.Grouping, values []sqltype.Value) (lock.Resource, func(), error) {
	hash, err := index.UniqueKeyHash(ctx, ordering, grouping, values)
	if err != nil {
		return lock.Resource{}, nil, err
	}
	return e.locks.PinUniqueKey(ctx, ticket, ref, hash, &index.UniqueKeyValues{Ordering: ordering, Values: values})
}
