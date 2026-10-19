package engine

import (
	"errors"
	"time"

	"stuntgres/internal/engine/ids"
)

// Engine.mu owns identities, reserved history slots and the last-completion cache.
// A reserved slot is absent until the row/catalog publisher installs its commit.
type commitTimestampEntry struct {
	Stamp   time.Time
	Origin  uint32
	Present bool
}

// assignTransactionXIDLocked fences ref against the current catalog epoch.
func (e *Engine) assignTransactionXIDLocked(ref ids.TxRef) error {
	if ref.Domain() != domainOf(e) || ref.Epoch() != e.catalog.state.Epoch() {
		return ids.ErrCatalogReplaced
	}
	return e.visibility.AssignTransactionXIDLocked(ref)
}

// assignTransactionXIDLocked gives ref its canonical full XID once, reserving
// its commit timestamp slot. Caller holds Engine.mu and has fenced ref.
func (reg *visibilityRegistry) AssignTransactionXIDLocked(ref ids.TxRef) error {
	v := reg.Entries[ref]
	if v.AssignedXID != 0 {
		return nil
	}
	full, err := nextFullTransactionID(reg.assignedXID)
	if err != nil {
		return err
	}
	xid := uint32(full)
	if prior, ok := reg.LowIDs[xid]; ok {
		record := reg.History[prior]
		live := record.Ticket == nil && record.State == ids.TxInProgress
		if record.Ticket != nil {
			live = !record.Ticket.OwnershipEnded() && record.Ticket.AssignedFullXID() == prior
		}
		if live {
			return errors.New("stuntgres: live transaction identity collision")
		}
	}
	if reg.trackCommitTimestamp {
		if reg.CommitTimestamps == nil {
			reg.CommitTimestamps = make(map[uint64]commitTimestampEntry)
		}
		if full > 0xffffffff {
			delete(reg.CommitTimestamps, full-(1<<32))
		}
		reg.CommitTimestamps[full] = commitTimestampEntry{}
	}
	if reg.Entries == nil {
		reg.Entries = make(map[ids.TxRef]transactionVisibility)
	}
	if reg.History == nil {
		reg.History = make(map[uint64]transactionIdentityRecord)
	}
	reg.History[full] = transactionIdentityRecord{Ticket: v.Ticket}
	if reg.LowIDs == nil {
		reg.LowIDs = make(map[uint32]uint64)
	}
	reg.LowIDs[xid] = full
	reg.assignedXID = full
	v.AssignedXID, v.FullXID = xid, full
	if v.Ticket != nil {
		v.Ticket.MirrorFullXID(full)
		v.Ticket.MirrorCompletion(ids.TxInProgress)
	}
	reg.Entries[ref] = v
	if node := reg.Serializable.Nodes[ref]; node != nil {
		node.AssignedXID = xid
	}
	return nil
}

// All storage is reserved before the publisher's final no-failure region.
// Consume completion ownership so an already-published utility cannot record twice.
func (reg *visibilityRegistry) RecordCommitTimestampLocked(ref ids.TxRef) {
	v := reg.Entries[ref]
	if v.AssignedXID == 0 {
		return
	}
	reg.EndTransactionIdentityLocked(ref, true)
	if reg.trackCommitTimestamp {
		entry := commitTimestampEntry{Stamp: time.Now().UTC().Truncate(time.Microsecond), Present: true}
		reg.CommitTimestamps[v.FullXID] = entry
		reg.lastCommittedXID, reg.lastCommitTimestamp = v.AssignedXID, entry
	}
	v.AssignedXID = 0
	v.FullXID = 0
	reg.Entries[ref] = v
}

func (reg *visibilityRegistry) RetireCommitTimestampsLocked() {
	reg.CommitTimestamps = nil
	reg.commitTimestampBase = uint64(reg.assignedXID) + 1
	reg.lastCommittedXID, reg.lastCommitTimestamp = 0, commitTimestampEntry{}
	clear(reg.History)
	clear(reg.LowIDs)
	reg.transactionHorizon, _ = nextFullTransactionID(reg.assignedXID)
}

// tracksCommitTimestamps reports track_commit_timestamp, fixed at engine
// construction.
func (reg *visibilityRegistry) TracksCommitTimestamps() bool {
	return reg.trackCommitTimestamp
}

// XID input names the newest possible retained epoch only. An absent/aborted
// newest assignment never falls back to an older completion with the same low32.
func (reg *visibilityRegistry) LookupCommitTimestampLocked(xid uint32) commitTimestampEntry {
	full := reg.assignedXID&^uint64(0xffffffff) | uint64(xid)
	if full > reg.assignedXID && full >= 1<<32 {
		full -= 1 << 32
	}
	if full < reg.commitTimestampBase {
		return commitTimestampEntry{}
	}
	return reg.CommitTimestamps[full]
}
