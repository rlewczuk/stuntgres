package visibility

import (
	"errors"
	"stuntgres/internal/engine/ids"
)

// PreflightXIDsLocked reserves no state. The caller holds Engine.Mu through
// the following top/ancestor assignments, so exhaustion and low32 alias errors
// cannot partially assign a child stack.
func (reg *Registry) PreflightXIDsLocked(count int) error {
	next := reg.assignedXID
	for range count {
		var err error
		next, err = NextFullXID(next)
		if err != nil {
			return err
		}
		if prior, ok := reg.LowIDs[uint32(next)]; ok && reg.identityInProgress(prior) {
			return errors.New("stuntgres: live transaction identity collision")
		}
	}
	return nil
}

// AssignChildXIDLocked uses the same allocator as top transactions. Children
// share their parent's completion authority, not its MVCC/SSI/lock ownership.
// Call only after preflighting the entire stack under Engine.Mu.
func (reg *Registry) AssignChildXIDLocked(parent uint64) uint64 {
	full, _ := NextFullXID(reg.assignedXID)
	reg.assignedXID = full
	if reg.History == nil {
		reg.History = make(map[uint64]IdentityRecord)
	}
	if reg.LowIDs == nil {
		reg.LowIDs = make(map[uint32]uint64)
	}
	if reg.children == nil {
		reg.children = make(map[uint64][]uint64)
	}
	reg.History[full] = IdentityRecord{Parent: parent}
	reg.LowIDs[uint32(full)] = full
	reg.children[parent] = append(reg.children[parent], full)
	if reg.trackCommitTimestamp {
		if reg.CommitTimestamps == nil {
			reg.CommitTimestamps = make(map[uint64]CommitTimestamp)
		}
		if full > 0xffffffff {
			delete(reg.CommitTimestamps, full-(1<<32))
		}
		reg.CommitTimestamps[full] = CommitTimestamp{}
	}
	return full
}
func (reg *Registry) identityInProgress(full uint64) bool {
	for full != 0 {
		r, ok := reg.History[full]
		if !ok {
			return false
		}
		if r.Parent != 0 {
			if r.State == ids.TxAborted {
				return false
			}
			full = r.Parent
			continue
		}
		if r.Ticket != nil {
			return !r.Ticket.OwnershipEnded() && r.Ticket.AssignedFullXID() == full
		}
		return r.State == ids.TxInProgress
	}
	return false
}

// AbortChildLocked aborts a real subtree, including released descendants still
// linked to it. A repeated ROLLBACK TO later assigns a new generation.
func (reg *Registry) AbortChildLocked(full uint64) {
	r, ok := reg.History[full]
	if !ok || r.Parent == 0 {
		return
	}
	r.State = ids.TxAborted
	reg.History[full] = r
	reg.finishChildren(full, ids.TxAborted)
	reg.latestCompletedXID = max(reg.latestCompletedXID, full)
}
func (reg *Registry) finishChildren(parent uint64, state ids.TxCompletion) {
	for _, child := range reg.children[parent] {
		r := reg.History[child]
		if r.State == ids.TxInProgress {
			r.State = state
			reg.History[child] = r
		}
		reg.finishChildren(child, r.State)
		reg.latestCompletedXID = max(reg.latestCompletedXID, child)
	}
}
func (reg *Registry) recordChildTimestamps(parent uint64, stamp CommitTimestamp) {
	for _, child := range reg.children[parent] {
		if reg.History[child].State == ids.TxCommitted {
			reg.CommitTimestamps[child] = stamp
			reg.recordChildTimestamps(child, stamp)
		}
	}
}

// ReleaseChildLocked transfers a real released generation to the surviving
// ancestor. Its canonical status remains in progress until that ancestor ends.
func (reg *Registry) ReleaseChildLocked(full, parent uint64) {
	r, ok := reg.History[full]
	if !ok || r.Parent == 0 || r.State != ids.TxInProgress {
		return
	}
	if r.Parent != parent {
		old := reg.children[r.Parent]
		for i, child := range old {
			if child == full {
				copy(old[i:], old[i+1:])
				reg.children[r.Parent] = old[:len(old)-1]
				break
			}
		}
		reg.children[parent] = append(reg.children[parent], full)
		r.Parent = parent
	}
	r.Subcommitted = true
	reg.History[full] = r
}
