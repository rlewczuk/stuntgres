package lock

import (
	"context"

	"stuntgres/internal/engine/ids"
)

// PgrowlocksTuple is an MVCC-selected physical identity, never a payload copy.
type PgrowlocksTuple struct {
	Tuple            MultiTuple
	PrivateInsertion bool
}
type PgrowlocksMember struct {
	XID  uint32
	Mode MultiMode
	PID  int32
}
type PgrowlocksRecord struct {
	Tuple   MultiTuple
	Locker  uint32
	Multi   bool
	Members []PgrowlocksMember
}

// Pgrowlocks joins only exact visible versions. It observes current effective
// contributions separately from immutable raw xmax membership. No capture path
// assigns an XID, settles a queue or registers completion ownership.
func (m *Manager) Pgrowlocks(ctx context.Context, caller *Ticket, tuples []PgrowlocksTuple) ([]PgrowlocksRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.valid(caller) || caller.OwnershipEnded() {
		return nil, ids.ErrCatalogReplaced
	}
	var rows []PgrowlocksRecord
	for _, prefix := range tuples {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		key := prefix.Tuple
		if !m.validMultiTuple(caller, key) {
			return nil, ids.ErrCatalogReplaced
		}
		row := m.multiRows[key]
		if len(row.members) == 0 {
			continue
		}
		lockOnly, running, ownUpdater := true, false, false
		for _, member := range row.members {
			effective := member.owner != nil && !member.owner.OwnershipEnded() &&
				member.owner.CompletionState() != ids.TxAborted && member.owner.multiContributions[key]&(1<<member.Mode) != 0
			running = running || effective
			if member.Mode.updating() && !row.inheritedSingleton {
				lockOnly = false
				ownUpdater = ownUpdater || effective && member.owner == caller
			}
		}
		// HTSU's private-insertion singleton branch is stricter than its
		// committed-insertion and Multi branches. Visibility alone is not enough.
		if !running || !lockOnly && (ownUpdater || row.id == 0 && prefix.PrivateInsertion) {
			continue
		}
		record := PgrowlocksRecord{
			Tuple: key, Locker: row.id, Multi: row.id != 0,
			Members: make([]PgrowlocksMember, len(row.members)),
		}
		var lastXID uint32
		var pid int32
		for i, member := range row.members {
			mode := member.Mode
			if row.inheritedSingleton {
				mode = MultiKeyShare
			}
			// Canonical raw bags group statuses by XID. Sample each top-level
			// identity once per tuple, including repeated statuses of one owner.
			if member.XID != lastXID {
				var err error
				pid, err = m.pgrowlocksPID(member)
				if err != nil {
					return nil, err
				}
				lastXID = member.XID
			}
			record.Members[i] = PgrowlocksMember{XID: member.XID, Mode: mode, PID: pid}
			if !record.Multi {
				record.Locker = member.XID
			}
		}
		rows = append(rows, record)
	}
	return rows, nil
}

// Manager.mu is held. BackendXidGetPid is a currently assigned top-level
// identity lookup: neither retained membership nor an old session ID suffices.
func (m *Manager) pgrowlocksPID(member MultiMember) (int32, error) {
	ticket := member.owner
	if ticket == nil || !m.valid(ticket) || ticket.detached || ticket.OwnershipEnded() ||
		ticket.CompletionState() == ids.TxAborted || ticket.AssignedPublicXID() != member.XID {
		return 0, nil
	}
	owner, visible, err := m.pgLockOwner(ticket)
	if err != nil || !visible {
		return 0, err
	}
	return int32(owner.Session), nil
}
