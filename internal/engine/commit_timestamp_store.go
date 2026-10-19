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

func (e *Engine) assignTransactionXIDLocked(ref ids.TxRef) error {
	if ref.Domain() != domainOf(e) || ref.Epoch() != e.catalog.state.Epoch() {
		return ids.ErrCatalogReplaced
	}
	v := e.visibility.entries[ref]
	if v.assignedXID != 0 {
		return nil
	}
	full, err := nextFullTransactionID(e.visibility.assignedXID)
	if err != nil {
		return err
	}
	xid := uint32(full)
	if prior, ok := e.visibility.assignedLowIDs[xid]; ok {
		record := e.visibility.transactionHistory[prior]
		live := record.ticket == nil && record.state == ids.TxInProgress
		if record.ticket != nil {
			live = !record.ticket.OwnershipEnded() && record.ticket.AssignedFullXID() == prior
		}
		if live {
			return errors.New("stuntgres: live transaction identity collision")
		}
	}
	if e.visibility.trackCommitTimestamp {
		if e.visibility.commitTimestamps == nil {
			e.visibility.commitTimestamps = make(map[uint64]commitTimestampEntry)
		}
		if full > 0xffffffff {
			delete(e.visibility.commitTimestamps, full-(1<<32))
		}
		e.visibility.commitTimestamps[full] = commitTimestampEntry{}
	}
	if e.visibility.entries == nil {
		e.visibility.entries = make(map[ids.TxRef]transactionVisibility)
	}
	if e.visibility.transactionHistory == nil {
		e.visibility.transactionHistory = make(map[uint64]transactionIdentityRecord)
	}
	e.visibility.transactionHistory[full] = transactionIdentityRecord{ticket: v.ticket}
	if e.visibility.assignedLowIDs == nil {
		e.visibility.assignedLowIDs = make(map[uint32]uint64)
	}
	e.visibility.assignedLowIDs[xid] = full
	e.visibility.assignedXID = full
	v.assignedXID, v.fullXID = xid, full
	if v.ticket != nil {
		v.ticket.MirrorFullXID(full)
		v.ticket.MirrorCompletion(ids.TxInProgress)
	}
	e.visibility.entries[ref] = v
	if node := e.visibility.serializable.nodes[ref]; node != nil {
		node.assignedXID = xid
	}
	return nil
}

// All storage is reserved before the publisher's final no-failure region.
// Consume completion ownership so an already-published utility cannot record twice.
func (e *Engine) recordCommitTimestampLocked(ref ids.TxRef) {
	v := e.visibility.entries[ref]
	if v.assignedXID == 0 {
		return
	}
	e.endTransactionIdentityLocked(ref, true)
	if e.visibility.trackCommitTimestamp {
		entry := commitTimestampEntry{stamp: time.Now().UTC().Truncate(time.Microsecond), present: true}
		e.visibility.commitTimestamps[v.fullXID] = entry
		e.visibility.lastCommittedXID, e.visibility.lastCommitTimestamp = v.assignedXID, entry
	}
	v.assignedXID = 0
	v.fullXID = 0
	e.visibility.entries[ref] = v
}

func (e *Engine) retireCommitTimestampsLocked() {
	e.visibility.commitTimestamps = nil
	e.visibility.commitTimestampBase = uint64(e.visibility.assignedXID) + 1
	e.visibility.lastCommittedXID, e.visibility.lastCommitTimestamp = 0, commitTimestampEntry{}
	clear(e.visibility.transactionHistory)
	clear(e.visibility.assignedLowIDs)
	e.visibility.transactionHorizon, _ = nextFullTransactionID(e.visibility.assignedXID)
}

// XID input names the newest possible retained epoch only. An absent/aborted
// newest assignment never falls back to an older completion with the same low32.
func (e *Engine) lookupCommitTimestampLocked(xid uint32) commitTimestampEntry {
	full := e.visibility.assignedXID&^uint64(0xffffffff) | uint64(xid)
	if full > e.visibility.assignedXID && full >= 1<<32 {
		full -= 1 << 32
	}
	if full < e.visibility.commitTimestampBase {
		return commitTimestampEntry{}
	}
	return e.visibility.commitTimestamps[full]
}
