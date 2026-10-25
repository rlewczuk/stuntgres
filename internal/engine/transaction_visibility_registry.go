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
	ticket, err := e.Locks.Register(ref)
	if err != nil {
		return nil, err
	}
	e.Mu.Lock()
	if ref.Epoch() != e.Catalog.State.Epoch() {
		e.Mu.Unlock()
		e.EndLockOwner(ticket)
		return nil, ids.ErrCatalogReplaced
	}
	e.Visibility.AttachTicketLocked(ref, ticket)
	e.Mu.Unlock()
	return ticket, nil
}

// VisibilityWriteLocked fences ref against this engine's current catalog
// epoch before the registry orders it as a writer.
func (e *Engine) VisibilityWriteLocked(ref ids.TxRef) error {
	if ref.Domain() != DomainOf(e) || ref.Epoch() != e.Catalog.State.Epoch() {
		return ids.ErrCatalogReplaced
	}
	return e.Visibility.WriteLocked(ref)
}

// VisibilitySnapshotLocked fences ref against this engine's current catalog
// epoch before the registry advertises its snapshot.
func (e *Engine) VisibilitySnapshotLocked(ref ids.TxRef, safeBuilder bool) (uint64, error) {
	if ref.Domain() != DomainOf(e) || ref.Epoch() != e.Catalog.State.Epoch() {
		return 0, ids.ErrCatalogReplaced
	}
	return e.Visibility.SnapshotLocked(ref, safeBuilder)
}

func (e *Engine) endStatementVisibility(ref ids.TxRef, stable bool) {
	if stable {
		return
	}
	e.Mu.Lock()
	e.Visibility.DropSnapshotLocked(ref)
	e.Mu.Unlock()
}

func (s *StatementScope) waitOlderIndexSnapshots(xmin uint64, idx ids.ObjectAddress) error {
	e := s.Engine()
	e.Mu.Lock()
	if s.TxRef.Epoch() != e.Catalog.State.Epoch() {
		e.Mu.Unlock()
		return ids.ErrCatalogReplaced
	}
	// Drop every advertised builder phase before collecting the real wait cohort.
	e.Visibility.ResetPhaseLocked(s.TxRef)
	refs := e.Visibility.OlderSnapshotsLocked(s.TxRef, xmin)
	// Publish only after dropping validation ownership and freezing the cohort,
	// under the same critical section used by the sole coordinator observation.
	if err := e.admitIndexBuildWaitLocked(idx, s.TxRef, index.WaitOlderSnapshots); err != nil {
		e.Mu.Unlock()
		return err
	}
	e.Mu.Unlock()
	defer e.clearIndexBuildWait(idx, s.TxRef, index.WaitOlderSnapshots)
	for _, ref := range refs {
		e.Mu.Lock()
		eligible := e.Visibility.SnapshotOlderLocked(ref, xmin)
		e.Mu.Unlock()
		if !eligible {
			continue
		}
		if owner := e.Locks.TransactionTicket(ref); owner != nil {
			if err := s.WriteHost().WaitTransaction(owner, lock.CompletionVirtual); err != nil {
				return err
			}
		}
	}
	return nil
}

// AssignTransactionXIDLocked fences ref against the current catalog epoch.
func (e *Engine) AssignTransactionXIDLocked(ref ids.TxRef) error {
	if ref.Domain() != DomainOf(e) || ref.Epoch() != e.Catalog.State.Epoch() {
		return ids.ErrCatalogReplaced
	}
	return e.Visibility.AssignTransactionXIDLocked(ref)
}

// transactionAgeLocked fences ref against the current catalog epoch.
func (e *Engine) transactionAgeLocked(ref ids.TxRef, xid uint32) (int32, error) {
	if ref.Domain() != DomainOf(e) || ref.Epoch() != e.Catalog.State.Epoch() {
		return 0, ids.ErrCatalogReplaced
	}
	return e.Visibility.TransactionAgeLocked(ref, xid)
}

// pinUniqueKey hashes the claimed values, then pins their interned lock
// resource. Hashing and comparison run outside every metadata mutex.
func (e *Engine) pinUniqueKey(ctx context.Context, ticket *lock.Ticket, ref ids.ObjectRef, ordering index.Ordering, grouping []sqltype.Grouping, values []sqltype.Value) (lock.Resource, func(), error) {
	hash, err := index.UniqueKeyHash(ctx, ordering, grouping, values)
	if err != nil {
		return lock.Resource{}, nil, err
	}
	return e.Locks.PinUniqueKey(ctx, ticket, ref, hash, &index.UniqueKeyValues{Ordering: ordering, Values: values})
}
