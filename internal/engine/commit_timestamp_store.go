package engine

import (
	"errors"
	"time"

	"stuntgres/internal/engine/ids"
)

// Engine.mu owns identities, reserved history slots and the last-completion cache.
// A reserved slot is absent until the row/catalog publisher installs its commit.
type commitTimestampEntry struct {
	stamp   time.Time
	origin  uint32
	present bool
}

// assignTransactionXIDLocked fences ref against the current catalog epoch.
func (e *Engine) assignTransactionXIDLocked(ref ids.TxRef) error {
	if ref.Domain() != domainOf(e) || ref.Epoch() != e.catalog.state.Epoch() {
		return ids.ErrCatalogReplaced
	}
	return e.visibility.assignTransactionXIDLocked(ref)
}

// assignTransactionXIDLocked gives ref its canonical full XID once, reserving
// its commit timestamp slot. Caller holds Engine.mu and has fenced ref.
func (reg *visibilityRegistry) assignTransactionXIDLocked(ref ids.TxRef) error {
	v := reg.entries[ref]
	if v.assignedXID != 0 {
		return nil
	}
	full, err := nextFullTransactionID(reg.assignedXID)
	if err != nil {
		return err
	}
	xid := uint32(full)
	if prior, ok := reg.assignedLowIDs[xid]; ok {
		record := reg.transactionHistory[prior]
		live := record.ticket == nil && record.state == ids.TxInProgress
		if record.ticket != nil {
			live = !record.ticket.OwnershipEnded() && record.ticket.AssignedFullXID() == prior
		}
		if live {
			return errors.New("stuntgres: live transaction identity collision")
		}
	}
	if reg.trackCommitTimestamp {
		if reg.commitTimestamps == nil {
			reg.commitTimestamps = make(map[uint64]commitTimestampEntry)
		}
		if full > 0xffffffff {
			delete(reg.commitTimestamps, full-(1<<32))
		}
		reg.commitTimestamps[full] = commitTimestampEntry{}
	}
	if reg.entries == nil {
		reg.entries = make(map[ids.TxRef]transactionVisibility)
	}
	if reg.transactionHistory == nil {
		reg.transactionHistory = make(map[uint64]transactionIdentityRecord)
	}
	reg.transactionHistory[full] = transactionIdentityRecord{ticket: v.ticket}
	if reg.assignedLowIDs == nil {
		reg.assignedLowIDs = make(map[uint32]uint64)
	}
	reg.assignedLowIDs[xid] = full
	reg.assignedXID = full
	v.assignedXID, v.fullXID = xid, full
	if v.ticket != nil {
		v.ticket.MirrorFullXID(full)
		v.ticket.MirrorCompletion(ids.TxInProgress)
	}
	reg.entries[ref] = v
	if node := reg.serializable.nodes[ref]; node != nil {
		node.assignedXID = xid
	}
	return nil
}

// All storage is reserved before the publisher's final no-failure region.
// Consume completion ownership so an already-published utility cannot record twice.
func (reg *visibilityRegistry) recordCommitTimestampLocked(ref ids.TxRef) {
	v := reg.entries[ref]
	if v.assignedXID == 0 {
		return
	}
	reg.endTransactionIdentityLocked(ref, true)
	if reg.trackCommitTimestamp {
		entry := commitTimestampEntry{stamp: time.Now().UTC().Truncate(time.Microsecond), present: true}
		reg.commitTimestamps[v.fullXID] = entry
		reg.lastCommittedXID, reg.lastCommitTimestamp = v.assignedXID, entry
	}
	v.assignedXID = 0
	v.fullXID = 0
	reg.entries[ref] = v
}

func (reg *visibilityRegistry) retireCommitTimestampsLocked() {
	reg.commitTimestamps = nil
	reg.commitTimestampBase = uint64(reg.assignedXID) + 1
	reg.lastCommittedXID, reg.lastCommitTimestamp = 0, commitTimestampEntry{}
	clear(reg.transactionHistory)
	clear(reg.assignedLowIDs)
	reg.transactionHorizon, _ = nextFullTransactionID(reg.assignedXID)
}

// tracksCommitTimestamps reports track_commit_timestamp, fixed at engine
// construction.
func (reg *visibilityRegistry) tracksCommitTimestamps() bool {
	return reg.trackCommitTimestamp
}

// lastCommitTimestampLocked returns the newest recorded commit and its
// timestamp, or zero values before the first one. Caller holds Engine.mu.
func (reg *visibilityRegistry) lastCommitTimestampLocked() (uint32, commitTimestampEntry) {
	return reg.lastCommittedXID, reg.lastCommitTimestamp
}

// XID input names the newest possible retained epoch only. An absent/aborted
// newest assignment never falls back to an older completion with the same low32.
func (reg *visibilityRegistry) lookupCommitTimestampLocked(xid uint32) commitTimestampEntry {
	full := reg.assignedXID&^uint64(0xffffffff) | uint64(xid)
	if full > reg.assignedXID && full >= 1<<32 {
		full -= 1 << 32
	}
	if full < reg.commitTimestampBase {
		return commitTimestampEntry{}
	}
	return reg.commitTimestamps[full]
}
