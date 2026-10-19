package visibility

import (
	"errors"
	"time"

	"stuntgres/internal/engine/ids"
)

// Engine.mu owns identities, reserved history slots and the last-completion cache.
// A reserved slot is absent until the row/catalog publisher installs its commit.
type CommitTimestamp struct {
	Stamp   time.Time
	Origin  uint32
	Present bool
}

// AssignTransactionXIDLocked gives ref its canonical full XID once, reserving
// its commit timestamp slot. Caller holds Engine.mu and has fenced ref.
func (reg *Registry) AssignTransactionXIDLocked(ref ids.TxRef) error {
	v := reg.Entries[ref]
	if v.AssignedXID != 0 {
		return nil
	}
	full, err := NextFullXID(reg.assignedXID)
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
			reg.CommitTimestamps = make(map[uint64]CommitTimestamp)
		}
		if full > 0xffffffff {
			delete(reg.CommitTimestamps, full-(1<<32))
		}
		reg.CommitTimestamps[full] = CommitTimestamp{}
	}
	if reg.Entries == nil {
		reg.Entries = make(map[ids.TxRef]Entry)
	}
	if reg.History == nil {
		reg.History = make(map[uint64]IdentityRecord)
	}
	reg.History[full] = IdentityRecord{Ticket: v.Ticket}
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
func (reg *Registry) RecordCommitTimestampLocked(ref ids.TxRef) {
	v := reg.Entries[ref]
	if v.AssignedXID == 0 {
		return
	}
	reg.EndTransactionIdentityLocked(ref, true)
	if reg.trackCommitTimestamp {
		entry := CommitTimestamp{Stamp: time.Now().UTC().Truncate(time.Microsecond), Present: true}
		reg.CommitTimestamps[v.FullXID] = entry
		reg.lastCommittedXID, reg.lastCommitTimestamp = v.AssignedXID, entry
	}
	v.AssignedXID = 0
	v.FullXID = 0
	reg.Entries[ref] = v
}

// RetireCommitTimestampsLocked forgets every commit timestamp and identity
// record at a catalog replacement; later lookups start after the current
// assignment frontier. Caller holds Engine.mu.
func (reg *Registry) RetireCommitTimestampsLocked() {
	reg.CommitTimestamps = nil
	reg.commitTimestampBase = uint64(reg.assignedXID) + 1
	reg.lastCommittedXID, reg.lastCommitTimestamp = 0, CommitTimestamp{}
	clear(reg.History)
	clear(reg.LowIDs)
	reg.transactionHorizon, _ = NextFullXID(reg.assignedXID)
}

// TracksCommitTimestamps reports track_commit_timestamp, fixed at engine
// construction.
func (reg *Registry) TracksCommitTimestamps() bool {
	return reg.trackCommitTimestamp
}

// XID input names the newest possible retained epoch only. An absent/aborted
// newest assignment never falls back to an older completion with the same low32.
func (reg *Registry) LookupCommitTimestampLocked(xid uint32) CommitTimestamp {
	full := reg.assignedXID&^uint64(0xffffffff) | uint64(xid)
	if full > reg.assignedXID && full >= 1<<32 {
		full -= 1 << 32
	}
	if full < reg.commitTimestampBase {
		return CommitTimestamp{}
	}
	return reg.CommitTimestamps[full]
}
