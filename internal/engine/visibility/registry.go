package visibility

import (
	"errors"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

// Entry is one transaction incarnation's visibility state. Engine.mu owns
// visibility/completion metadata. The ticket is an atomic mirror
// association only, not permission to enter manager metadata. Visibility XIDs
// order incarnations globally; CommitSeq continues to own data views.
type Entry struct {
	XID, Xmin             uint64
	AssignedXID           uint32
	FullXID               uint64
	AgeAnchor             uint32
	AgeDemanded           bool
	Snapshot, SafeBuilder bool
	Ticket                *lock.Ticket // assignment mirror only; never take manager.mu under Engine.mu
}

// Registry owns transaction identity, commit order, commit
// timestamps and the serializable tracker. It has no mutex: the caller holds
// Engine.mu for every read and write, so publication stays atomic with the
// catalog and storage. Engine holds it by value and never replaces it whole.
// Its methods never take another mutex, block or call back into the engine.
type Registry struct {
	commitSeq            ids.CommitSeq
	diagnosticTx         uint32 // Last TxRef diagnostic (virtual transaction) identity.
	visibilityXID        uint64
	Entries              map[ids.TxRef]Entry
	assignedXID          uint64
	latestCompletedXID   uint64
	History              map[uint64]IdentityRecord
	transactionHorizon   uint64
	LowIDs               map[uint32]uint64 // Latest full assignment per low32; constant-time live alias guard.
	trackCommitTimestamp bool
	commitTimestampBase  uint64
	CommitTimestamps     map[uint64]CommitTimestamp
	lastCommittedXID     uint32
	lastCommitTimestamp  CommitTimestamp
	Serializable         Tracker
}

// NewRegistry starts canonical assignment after the special XIDs.
// Maps are allocated on first use.
func NewRegistry(trackCommitTimestamp bool) Registry {
	return Registry{trackCommitTimestamp: trackCommitTimestamp, assignedXID: 2, latestCompletedXID: 2, commitTimestampBase: 3}
}

// CommitSeq is the newest published commit frontier. Caller holds Engine.mu.
func (reg *Registry) CommitSeq() ids.CommitSeq { return reg.commitSeq }

// AdvanceCommitSeqLocked publishes the next commit frontier by one step. The
// caller has already checked exhaustion with its own diagnostic and holds
// Engine.mu inside the publication's no-failure region.
func (reg *Registry) AdvanceCommitSeqLocked() { reg.commitSeq++ }

// SetCommitSeqLocked seeds the commit frontier. It is a test and diagnostic
// seam; publication advances with AdvanceCommitSeqLocked. Caller holds Engine.mu.
func (reg *Registry) SetCommitSeqLocked(seq ids.CommitSeq) { reg.commitSeq = seq }

// VisibilityXID is the newest writer ordering identity. Caller holds Engine.mu.
func (reg *Registry) VisibilityXID() uint64 { return reg.visibilityXID }

// LastAssignedXID is the newest canonical full XID, 2 before any assignment.
// Caller holds Engine.mu.
func (reg *Registry) LastAssignedXID() uint64 { return reg.assignedXID }

// SetLastAssignedXIDLocked seeds the canonical XID frontier for exhaustion and
// wraparound tests; production assigns through AssignTransactionXIDLocked.
// Caller holds Engine.mu.
func (reg *Registry) SetLastAssignedXIDLocked(full uint64) { reg.assignedXID = full }

// DiagnosticTx is the last TxRef diagnostic identity. Caller holds Engine.mu.
func (reg *Registry) DiagnosticTx() uint32 { return reg.diagnosticTx }

// SetDiagnosticTxLocked seeds the TxRef diagnostic counter for exhaustion
// tests; production allocates through NextDiagnosticTxLocked. Caller holds
// Engine.mu.
func (reg *Registry) SetDiagnosticTxLocked(id uint32) { reg.diagnosticTx = id }

// CommitTimestampBase is the oldest full XID whose commit timestamp can still
// be reported. Caller holds Engine.mu.
func (reg *Registry) CommitTimestampBase() uint64 { return reg.commitTimestampBase }

// LastCommittedXID is the public XID of the newest recorded commit, zero
// before the first one. Caller holds Engine.mu.
func (reg *Registry) LastCommittedXID() uint32 { return reg.lastCommittedXID }

// LastCommitTimestamp is the newest recorded commit timestamp. Caller holds
// Engine.mu.
func (reg *Registry) LastCommitTimestamp() CommitTimestamp {
	return reg.lastCommitTimestamp
}

// SerializableOrder is the tracker's last finalization order. Caller holds
// Engine.mu.
func (reg *Registry) SerializableOrder() uint64 { return reg.Serializable.order }

// AttachTicketLocked associates ref's registered lock ticket and mirrors any
// canonical assignment onto it. Only ticket atomics are written, never
// manager metadata. Caller holds Engine.mu.
func (reg *Registry) AttachTicketLocked(ref ids.TxRef, ticket *lock.Ticket) {
	if reg.Entries == nil {
		reg.Entries = make(map[ids.TxRef]Entry)
	}
	v := reg.Entries[ref]
	v.Ticket = ticket
	ticket.MirrorFullXID(v.FullXID)
	reg.Entries[ref] = v
}

// WriteLocked gives ref its visibility XID on first write. Caller holds
// Engine.mu and has fenced ref.
func (reg *Registry) WriteLocked(ref ids.TxRef) error {
	v := reg.Entries[ref]
	if v.XID != 0 {
		return nil
	}
	if reg.visibilityXID == ^uint64(0) {
		return errors.New("stuntgres: visibility identity exhausted")
	}
	reg.visibilityXID++
	v.XID = reg.visibilityXID
	if reg.Entries == nil {
		reg.Entries = make(map[ids.TxRef]Entry)
	}
	reg.Entries[ref] = v
	return nil
}

// SnapshotLocked advertises ref's snapshot once and returns its xmin, the
// oldest writer of the same epoch. Caller holds Engine.mu and has fenced ref.
func (reg *Registry) SnapshotLocked(ref ids.TxRef, safeBuilder bool) (uint64, error) {
	v := reg.Entries[ref]
	if v.Snapshot {
		return v.Xmin, nil
	}
	if reg.visibilityXID == ^uint64(0) {
		return 0, errors.New("stuntgres: visibility identity exhausted")
	}
	xmin := reg.visibilityXID + 1
	for other, state := range reg.Entries {
		if other.Epoch() == ref.Epoch() && state.XID != 0 && state.XID < xmin {
			xmin = state.XID
		}
	}
	v.Xmin, v.Snapshot, v.SafeBuilder = xmin, true, safeBuilder
	if reg.Entries == nil {
		reg.Entries = make(map[ids.TxRef]Entry)
	}
	reg.Entries[ref] = v
	return xmin, nil
}

// WithdrawSnapshotLocked clears ref's snapshot flag so the next
// SnapshotLocked recaptures xmin. Caller holds Engine.mu.
func (reg *Registry) WithdrawSnapshotLocked(ref ids.TxRef) {
	v := reg.Entries[ref]
	v.Snapshot = false
	reg.Entries[ref] = v
}

// Snapshot advertisement ends independently of writer/completion ownership.
// Engine.mu is held; PREPARE retains the detached incarnation's assigned XID.
func (reg *Registry) DropSnapshotLocked(ref ids.TxRef) {
	if v, ok := reg.Entries[ref]; ok {
		v.Xmin, v.Snapshot, v.SafeBuilder = 0, false, false
		if v.XID == 0 && v.AssignedXID == 0 && v.Ticket == nil {
			delete(reg.Entries, ref)
		} else {
			reg.Entries[ref] = v
		}
	}
}

// An index publication phase retires advertisement, not its registered ticket.
// Preserve canonical assignment and its exact mirror association for later work.
// Engine.mu is held; lifecycle end/abort/fence still remove the whole entry.
func (reg *Registry) ResetPhaseLocked(ref ids.TxRef) {
	if v, ok := reg.Entries[ref]; ok {
		if v.Ticket != nil {
			// These index-only hooks retire a completed internal publication
			// phase. Final/prepared completion instead keeps manager ownership
			// through end(), including the publication-to-notification interval.
			v.Ticket.MirrorFullXID(v.FullXID)
		}
		if v.AssignedXID == 0 && v.Ticket == nil {
			delete(reg.Entries, ref)
		} else {
			reg.Entries[ref] = Entry{AssignedXID: v.AssignedXID, FullXID: v.FullXID, Ticket: v.Ticket}
		}
	}
}

// OlderSnapshotsLocked lists the other transactions of self's epoch that
// advertise a non-builder snapshot no newer than xmin. Caller holds Engine.mu.
func (reg *Registry) OlderSnapshotsLocked(self ids.TxRef, xmin uint64) []ids.TxRef {
	var refs []ids.TxRef
	for ref := range reg.Entries {
		if ref != self && ref.Epoch() == self.Epoch() && reg.SnapshotOlderLocked(ref, xmin) {
			refs = append(refs, ref)
		}
	}
	return refs
}

// SnapshotOlderLocked reports whether ref still advertises a non-builder
// snapshot no newer than xmin. Caller holds Engine.mu.
func (reg *Registry) SnapshotOlderLocked(ref ids.TxRef, xmin uint64) bool {
	v, ok := reg.Entries[ref]
	return ok && v.Snapshot && !v.SafeBuilder && v.Xmin <= xmin
}
