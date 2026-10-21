package engine

import (
	"errors"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
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

func (s *StatementScope) waitOlderIndexSnapshots(xmin uint64, index ids.ObjectAddress) error {
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
	if err := e.admitIndexBuildWaitLocked(index, s.txRef, WaitOlderSnapshots); err != nil {
		e.mu.Unlock()
		return err
	}
	e.mu.Unlock()
	defer e.clearIndexBuildWait(index, s.txRef, WaitOlderSnapshots)
	for _, ref := range refs {
		e.mu.Lock()
		eligible := e.visibility.SnapshotOlderLocked(ref, xmin)
		e.mu.Unlock()
		if !eligible {
			continue
		}
		if owner := e.locks.TransactionTicket(ref); owner != nil {
			if err := s.waitCompletion(owner, lock.CompletionVirtual); err != nil {
				return err
			}
		}
	}
	return nil
}
