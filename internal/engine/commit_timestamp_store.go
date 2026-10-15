package engine

import (
	"errors"
	"time"
)

// Engine.mu owns identities, reserved history slots and the last-completion cache.
// A reserved slot is absent until the row/catalog publisher installs its commit.
type commitTimestampEntry struct {
	stamp   time.Time
	origin  uint32
	present bool
}

func (e *Engine) assignTransactionXIDLocked(ref TxRef) error {
	if ref.owner != e || ref.epoch != e.state.epoch {
		return ErrCatalogReplaced
	}
	v := e.visibility[ref]
	if v.assignedXID != 0 {
		return nil
	}
	if e.assignedXID == ^uint32(0) {
		return errors.New("stuntgres: transaction identity exhausted")
	}
	xid := e.assignedXID + 1
	if e.trackCommitTimestamp {
		index := uint64(xid) - e.commitTimestampBase
		if index >= uint64(^uint(0)>>1) {
			return errors.New("stuntgres: commit timestamp history exhausted")
		}
		needed := int(index) + 1
		if needed > cap(e.commitTimestamps) {
			capacity := max(needed, max(16, cap(e.commitTimestamps)*2))
			next := make([]commitTimestampEntry, needed, capacity)
			copy(next, e.commitTimestamps)
			e.commitTimestamps = next
		} else if needed > len(e.commitTimestamps) {
			e.commitTimestamps = e.commitTimestamps[:needed]
		}
	}
	if e.visibility == nil {
		e.visibility = make(map[TxRef]transactionVisibility)
	}
	e.assignedXID = xid
	v.assignedXID = xid
	if v.ticket != nil {
		v.ticket.mirrorAssignedXID(xid)
	}
	e.visibility[ref] = v
	if node := e.serializable.nodes[ref]; node != nil {
		node.assignedXID = xid
	}
	return nil
}

// All storage is reserved before the publisher's final no-failure region.
// Consume completion ownership so an already-published utility cannot record twice.
func (e *Engine) recordCommitTimestampLocked(ref TxRef) {
	v := e.visibility[ref]
	if v.assignedXID == 0 {
		return
	}
	if e.trackCommitTimestamp {
		entry := commitTimestampEntry{stamp: time.Now().UTC().Truncate(time.Microsecond), present: true}
		e.commitTimestamps[uint64(v.assignedXID)-e.commitTimestampBase] = entry
		e.lastCommittedXID, e.lastCommitTimestamp = v.assignedXID, entry
	}
	v.assignedXID = 0
	e.visibility[ref] = v
}

func (e *Engine) retireCommitTimestampsLocked() {
	e.commitTimestamps = nil
	e.commitTimestampBase = uint64(e.assignedXID) + 1
	e.lastCommittedXID, e.lastCommitTimestamp = 0, commitTimestampEntry{}
}
