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

// visibilityRegistry owns transaction identity, commit order, commit
// timestamps and the serializable tracker. It has no mutex: the caller holds
// Engine.mu for every read and write, so publication stays atomic with the
// catalog and storage. Engine holds it by value and never replaces it whole.
// Its methods never take another mutex, block or call back into the engine.
type visibilityRegistry struct {
	commitSeq            ids.CommitSeq
	diagnosticTx         uint32 // Last TxRef diagnostic (virtual transaction) identity.
	visibilityXID        uint64
	entries              map[ids.TxRef]transactionVisibility
	assignedXID          uint64
	transactionHistory   map[uint64]transactionIdentityRecord
	transactionHorizon   uint64
	assignedLowIDs       map[uint32]uint64 // Latest full assignment per low32; constant-time live alias guard.
	trackCommitTimestamp bool
	commitTimestampBase  uint64
	commitTimestamps     map[uint64]commitTimestampEntry
	lastCommittedXID     uint32
	lastCommitTimestamp  commitTimestampEntry
	serializable         serializableTracker
}

// newVisibilityRegistry starts canonical assignment after the special XIDs.
// Maps are allocated on first use.
func newVisibilityRegistry(trackCommitTimestamp bool) visibilityRegistry {
	return visibilityRegistry{trackCommitTimestamp: trackCommitTimestamp, assignedXID: 2, commitTimestampBase: 3}
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
	if ref.Epoch() != e.catalog.state.Epoch() {
		e.mu.Unlock()
		e.endLockOwner(ticket)
		return nil, ids.ErrCatalogReplaced
	}
	e.visibility.attachTicketLocked(ref, ticket)
	e.mu.Unlock()
	return ticket, nil
}

// attachTicketLocked associates ref's registered lock ticket and mirrors any
// canonical assignment onto it. Only ticket atomics are written, never
// manager metadata. Caller holds Engine.mu.
func (reg *visibilityRegistry) attachTicketLocked(ref ids.TxRef, ticket *lock.Ticket) {
	if reg.entries == nil {
		reg.entries = make(map[ids.TxRef]transactionVisibility)
	}
	v := reg.entries[ref]
	v.ticket = ticket
	ticket.MirrorFullXID(v.fullXID)
	reg.entries[ref] = v
}

// visibilityWriteLocked fences ref against this engine's current catalog
// epoch before the registry orders it as a writer.
func (e *Engine) visibilityWriteLocked(ref ids.TxRef) error {
	if ref.Domain() != domainOf(e) || ref.Epoch() != e.catalog.state.Epoch() {
		return ids.ErrCatalogReplaced
	}
	return e.visibility.writeLocked(ref)
}

// writeLocked gives ref its visibility XID on first write. Caller holds
// Engine.mu and has fenced ref.
func (reg *visibilityRegistry) writeLocked(ref ids.TxRef) error {
	v := reg.entries[ref]
	if v.xid != 0 {
		return nil
	}
	if reg.visibilityXID == ^uint64(0) {
		return errors.New("stuntgres: visibility identity exhausted")
	}
	reg.visibilityXID++
	v.xid = reg.visibilityXID
	if reg.entries == nil {
		reg.entries = make(map[ids.TxRef]transactionVisibility)
	}
	reg.entries[ref] = v
	return nil
}

// visibilitySnapshotLocked fences ref against this engine's current catalog
// epoch before the registry advertises its snapshot.
func (e *Engine) visibilitySnapshotLocked(ref ids.TxRef, safeBuilder bool) (uint64, error) {
	if ref.Domain() != domainOf(e) || ref.Epoch() != e.catalog.state.Epoch() {
		return 0, ids.ErrCatalogReplaced
	}
	return e.visibility.snapshotLocked(ref, safeBuilder)
}

// snapshotLocked advertises ref's snapshot once and returns its xmin, the
// oldest writer of the same epoch. Caller holds Engine.mu and has fenced ref.
func (reg *visibilityRegistry) snapshotLocked(ref ids.TxRef, safeBuilder bool) (uint64, error) {
	v := reg.entries[ref]
	if v.snapshot {
		return v.xmin, nil
	}
	if reg.visibilityXID == ^uint64(0) {
		return 0, errors.New("stuntgres: visibility identity exhausted")
	}
	xmin := reg.visibilityXID + 1
	for other, state := range reg.entries {
		if other.Epoch() == ref.Epoch() && state.xid != 0 && state.xid < xmin {
			xmin = state.xid
		}
	}
	v.xmin, v.snapshot, v.safeBuilder = xmin, true, safeBuilder
	if reg.entries == nil {
		reg.entries = make(map[ids.TxRef]transactionVisibility)
	}
	reg.entries[ref] = v
	return xmin, nil
}

// withdrawSnapshotLocked clears ref's snapshot flag so the next
// snapshotLocked recaptures xmin. Caller holds Engine.mu.
func (reg *visibilityRegistry) withdrawSnapshotLocked(ref ids.TxRef) {
	v := reg.entries[ref]
	v.snapshot = false
	reg.entries[ref] = v
}

func (e *Engine) endStatementVisibility(ref ids.TxRef, stable bool) {
	if stable {
		return
	}
	e.mu.Lock()
	e.visibility.dropSnapshotLocked(ref)
	e.mu.Unlock()
}

// Snapshot advertisement ends independently of writer/completion ownership.
// Engine.mu is held; PREPARE retains the detached incarnation's assigned XID.
func (reg *visibilityRegistry) dropSnapshotLocked(ref ids.TxRef) {
	if v, ok := reg.entries[ref]; ok {
		v.xmin, v.snapshot, v.safeBuilder = 0, false, false
		if v.xid == 0 && v.assignedXID == 0 && v.ticket == nil {
			delete(reg.entries, ref)
		} else {
			reg.entries[ref] = v
		}
	}
}

// An index publication phase retires advertisement, not its registered ticket.
// Preserve canonical assignment and its exact mirror association for later work.
// Engine.mu is held; lifecycle end/abort/fence still remove the whole entry.
func (reg *visibilityRegistry) resetPhaseLocked(ref ids.TxRef) {
	if v, ok := reg.entries[ref]; ok {
		if v.ticket != nil {
			// These index-only hooks retire a completed internal publication
			// phase. Final/prepared completion instead keeps manager ownership
			// through end(), including the publication-to-notification interval.
			v.ticket.MirrorFullXID(v.fullXID)
		}
		if v.assignedXID == 0 && v.ticket == nil {
			delete(reg.entries, ref)
		} else {
			reg.entries[ref] = transactionVisibility{assignedXID: v.assignedXID, fullXID: v.fullXID, ticket: v.ticket}
		}
	}
}

func (s *StatementScope) waitOlderIndexSnapshots(xmin uint64, index ids.ObjectAddress) error {
	e := s.op.session.Engine()
	e.mu.Lock()
	if s.txRef.Epoch() != e.catalog.state.Epoch() {
		e.mu.Unlock()
		return ids.ErrCatalogReplaced
	}
	// Drop every advertised builder phase before collecting the real wait cohort.
	e.visibility.resetPhaseLocked(s.txRef)
	refs := e.visibility.olderSnapshotsLocked(s.txRef, xmin)
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
		eligible := e.visibility.snapshotOlderLocked(ref, xmin)
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

// olderSnapshotsLocked lists the other transactions of self's epoch that
// advertise a non-builder snapshot no newer than xmin. Caller holds Engine.mu.
func (reg *visibilityRegistry) olderSnapshotsLocked(self ids.TxRef, xmin uint64) []ids.TxRef {
	var refs []ids.TxRef
	for ref := range reg.entries {
		if ref != self && ref.Epoch() == self.Epoch() && reg.snapshotOlderLocked(ref, xmin) {
			refs = append(refs, ref)
		}
	}
	return refs
}

// snapshotOlderLocked reports whether ref still advertises a non-builder
// snapshot no newer than xmin. Caller holds Engine.mu.
func (reg *visibilityRegistry) snapshotOlderLocked(ref ids.TxRef, xmin uint64) bool {
	v, ok := reg.entries[ref]
	return ok && v.snapshot && !v.safeBuilder && v.xmin <= xmin
}
