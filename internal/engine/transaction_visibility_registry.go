package engine

import (
	"errors"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

// Engine.mu owns visibility/completion metadata. The ticket is an atomic mirror
// association only, not permission to enter manager metadata. Visibility XIDs
// order incarnations globally; CommitSeq continues to own data views.
type transactionVisibility struct {
	xid, xmin             uint64
	assignedXID           uint32
	fullXID               uint64
	ageAnchor             uint32
	ageDemanded           bool
	snapshot, safeBuilder bool
	ticket                *lock.Ticket // assignment mirror only; never take manager.mu under Engine.mu
}

// Admission serializes this incarnation. Register with the manager first, then
// associate its exact ticket before any command can reach canonical assignment.
// This function holds neither manager.mu nor TX synchronization while taking
// Engine.mu; the general order is documented on lock.Manager.
func (e *Engine) registerTransactionLocks(ref ids.TxRef) (*lock.Ticket, error) {
	if !txOwnerAttached(ref) {
		return nil, errors.New("stuntgres: invalid lock owner")
	}
	ticket, err := e.locks.Register(ref)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	if ref.Epoch() != e.catalog.state.epoch {
		e.mu.Unlock()
		e.endLockOwner(ticket)
		return nil, ids.ErrCatalogReplaced
	}
	if e.visibility == nil {
		e.visibility = make(map[ids.TxRef]transactionVisibility)
	}
	v := e.visibility[ref]
	v.ticket = ticket
	ticket.MirrorFullXID(v.fullXID)
	e.visibility[ref] = v
	e.mu.Unlock()
	return ticket, nil
}

func (e *Engine) visibilityWriteLocked(ref ids.TxRef) error {
	if ref.Domain() != domainOf(e) || ref.Epoch() != e.catalog.state.epoch {
		return ids.ErrCatalogReplaced
	}
	v := e.visibility[ref]
	if v.xid != 0 {
		return nil
	}
	if e.visibilityXID == ^uint64(0) {
		return errors.New("stuntgres: visibility identity exhausted")
	}
	e.visibilityXID++
	v.xid = e.visibilityXID
	if e.visibility == nil {
		e.visibility = make(map[ids.TxRef]transactionVisibility)
	}
	e.visibility[ref] = v
	return nil
}

func (e *Engine) visibilitySnapshotLocked(ref ids.TxRef, safeBuilder bool) (uint64, error) {
	if ref.Domain() != domainOf(e) || ref.Epoch() != e.catalog.state.epoch {
		return 0, ids.ErrCatalogReplaced
	}
	v := e.visibility[ref]
	if v.snapshot {
		return v.xmin, nil
	}
	if e.visibilityXID == ^uint64(0) {
		return 0, errors.New("stuntgres: visibility identity exhausted")
	}
	xmin := e.visibilityXID + 1
	for other, state := range e.visibility {
		if other.Epoch() == ref.Epoch() && state.xid != 0 && state.xid < xmin {
			xmin = state.xid
		}
	}
	v.xmin, v.snapshot, v.safeBuilder = xmin, true, safeBuilder
	if e.visibility == nil {
		e.visibility = make(map[ids.TxRef]transactionVisibility)
	}
	e.visibility[ref] = v
	return xmin, nil
}

func (e *Engine) endStatementVisibility(ref ids.TxRef, stable bool) {
	if stable {
		return
	}
	e.mu.Lock()
	e.dropVisibilitySnapshotLocked(ref)
	e.mu.Unlock()
}

// Snapshot advertisement ends independently of writer/completion ownership.
// Engine.mu is held; PREPARE retains the detached incarnation's assigned XID.
func (e *Engine) dropVisibilitySnapshotLocked(ref ids.TxRef) {
	if v, ok := e.visibility[ref]; ok {
		v.xmin, v.snapshot, v.safeBuilder = 0, false, false
		if v.xid == 0 && v.assignedXID == 0 && v.ticket == nil {
			delete(e.visibility, ref)
		} else {
			e.visibility[ref] = v
		}
	}
}

// An index publication phase retires advertisement, not its registered ticket.
// Preserve canonical assignment and its exact mirror association for later work.
// Engine.mu is held; lifecycle end/abort/fence still remove the whole entry.
func (e *Engine) resetVisibilityPhaseLocked(ref ids.TxRef) {
	if v, ok := e.visibility[ref]; ok {
		if v.ticket != nil {
			// These index-only hooks retire a completed internal publication
			// phase. Final/prepared completion instead keeps manager ownership
			// through end(), including the publication-to-notification interval.
			v.ticket.MirrorFullXID(v.fullXID)
		}
		if v.assignedXID == 0 && v.ticket == nil {
			delete(e.visibility, ref)
		} else {
			e.visibility[ref] = transactionVisibility{assignedXID: v.assignedXID, fullXID: v.fullXID, ticket: v.ticket}
		}
	}
}

func (s *StatementScope) waitOlderIndexSnapshots(xmin uint64, index ids.ObjectAddress) error {
	e := s.op.session.Engine()
	e.mu.Lock()
	if s.txRef.Epoch() != e.catalog.state.epoch {
		e.mu.Unlock()
		return ids.ErrCatalogReplaced
	}
	// Drop every advertised builder phase before collecting the real wait cohort.
	e.resetVisibilityPhaseLocked(s.txRef)
	var refs []ids.TxRef
	for ref, v := range e.visibility {
		if ref != s.txRef && ref.Epoch() == s.txRef.Epoch() && v.snapshot && !v.safeBuilder && v.xmin <= xmin {
			refs = append(refs, ref)
		}
	}
	// Publish only after dropping validation ownership and freezing the cohort,
	// under the same critical section used by the sole coordinator observation.
	if err := e.admitIndexBuildWaitLocked(index, s.txRef, indexWaitOlderSnapshots); err != nil {
		e.mu.Unlock()
		return err
	}
	e.mu.Unlock()
	defer e.clearIndexBuildWait(index, s.txRef, indexWaitOlderSnapshots)
	for _, ref := range refs {
		e.mu.Lock()
		v, ok := e.visibility[ref]
		eligible := ok && v.snapshot && !v.safeBuilder && v.xmin <= xmin
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
