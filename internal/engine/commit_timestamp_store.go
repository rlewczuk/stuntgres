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
	if ref.Domain() != domainOf(e) || ref.Epoch() != e.state.epoch {
		return ErrCatalogReplaced
	}
	v := e.visibility[ref]
	if v.assignedXID != 0 {
		return nil
	}
	full, err := nextFullTransactionID(e.assignedXID)
	if err != nil {
		return err
	}
	xid := uint32(full)
	if prior, ok := e.assignedLowIDs[xid]; ok {
		record := e.transactionHistory[prior]
		live := record.ticket == nil && record.state == ids.TxInProgress
		if record.ticket != nil {
			live = !record.ticket.ownershipEnded() && record.ticket.assignedFullXID() == prior
		}
		if live {
			return errors.New("stuntgres: live transaction identity collision")
		}
	}
	if e.trackCommitTimestamp {
		if e.commitTimestamps == nil {
			e.commitTimestamps = make(map[uint64]commitTimestampEntry)
		}
		if full > 0xffffffff {
			delete(e.commitTimestamps, full-(1<<32))
		}
		e.commitTimestamps[full] = commitTimestampEntry{}
	}
	if e.visibility == nil {
		e.visibility = make(map[ids.TxRef]transactionVisibility)
	}
	if e.transactionHistory == nil {
		e.transactionHistory = make(map[uint64]transactionIdentityRecord)
	}
	e.transactionHistory[full] = transactionIdentityRecord{ticket: v.ticket}
	if e.assignedLowIDs == nil {
		e.assignedLowIDs = make(map[uint32]uint64)
	}
	e.assignedLowIDs[xid] = full
	e.assignedXID = full
	v.assignedXID, v.fullXID = xid, full
	if v.ticket != nil {
		v.ticket.mirrorFullXID(full)
		v.ticket.mirrorCompletion(ids.TxInProgress)
	}
	e.visibility[ref] = v
	if node := e.serializable.nodes[ref]; node != nil {
		node.assignedXID = xid
	}
	return nil
}

// All storage is reserved before the publisher's final no-failure region.
// Consume completion ownership so an already-published utility cannot record twice.
func (e *Engine) recordCommitTimestampLocked(ref ids.TxRef) {
	v := e.visibility[ref]
	if v.assignedXID == 0 {
		return
	}
	e.endTransactionIdentityLocked(ref, true)
	if e.trackCommitTimestamp {
		entry := commitTimestampEntry{stamp: time.Now().UTC().Truncate(time.Microsecond), present: true}
		e.commitTimestamps[v.fullXID] = entry
		e.lastCommittedXID, e.lastCommitTimestamp = v.assignedXID, entry
	}
	v.assignedXID = 0
	v.fullXID = 0
	e.visibility[ref] = v
}

func (e *Engine) retireCommitTimestampsLocked() {
	e.commitTimestamps = nil
	e.commitTimestampBase = uint64(e.assignedXID) + 1
	e.lastCommittedXID, e.lastCommitTimestamp = 0, commitTimestampEntry{}
	clear(e.transactionHistory)
	clear(e.assignedLowIDs)
	e.transactionHorizon, _ = nextFullTransactionID(e.assignedXID)
}

// XID input names the newest possible retained epoch only. An absent/aborted
// newest assignment never falls back to an older completion with the same low32.
func (e *Engine) lookupCommitTimestampLocked(xid uint32) commitTimestampEntry {
	full := e.assignedXID&^uint64(0xffffffff) | uint64(xid)
	if full > e.assignedXID && full >= 1<<32 {
		full -= 1 << 32
	}
	if full < e.commitTimestampBase {
		return commitTimestampEntry{}
	}
	return e.commitTimestamps[full]
}
