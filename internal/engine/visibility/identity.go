package visibility

import (
	"errors"
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

// IdentityRecord is the completion state of one assigned full XID. Ticket,
// when set, is the owner whose atomics answer "in progress".
type IdentityRecord struct {
	State  ids.TxCompletion
	Ticket *lock.Ticket
}

// NextFullXID is the canonical full XID after last. Low 32-bit values below 3
// are special and skipped.
func NextFullXID(last uint64) (uint64, error) {
	if last == ^uint64(0) {
		return 0, errors.New("stuntgres: transaction identity exhausted")
	}
	next := last + 1
	if uint32(next) < 3 {
		if next > ^uint64(0)-3 {
			return 0, errors.New("stuntgres: transaction identity exhausted")
		}
		next = next&^uint64(0xffffffff) | 3
	}
	return next, nil
}

// Age is PostgreSQL's age(xid) relative to now; special XIDs are infinitely old.
func Age(now, xid uint32) int32 {
	if xid < 3 {
		return 2147483647
	}
	return int32(now - xid)
}

// TransactionAgeLocked anchors ref's age() once, at its assigned XID or the
// next one to be assigned. Caller holds Engine.mu and has fenced ref.
func (reg *Registry) TransactionAgeLocked(ref ids.TxRef, xid uint32) (int32, error) {
	v := reg.Entries[ref]
	if !v.AgeDemanded {
		anchor := v.AssignedXID
		if anchor == 0 {
			next, err := NextFullXID(reg.assignedXID)
			if err != nil {
				return 0, err
			}
			anchor = uint32(next)
		}
		v.AgeAnchor, v.AgeDemanded = anchor, true
		if reg.Entries == nil {
			reg.Entries = make(map[ids.TxRef]Entry)
		}
		reg.Entries[ref] = v
	}
	return Age(v.AgeAnchor, xid), nil
}

// AssignedXIDLocked is ref's public low-32 XID, zero when unassigned.
// Caller holds Engine.mu.
func (reg *Registry) AssignedXIDLocked(ref ids.TxRef) uint32 {
	return reg.Entries[ref].AssignedXID
}

// FullXIDLocked is ref's canonical full XID, zero when unassigned. Caller
// holds Engine.mu.
func (reg *Registry) FullXIDLocked(ref ids.TxRef) uint64 {
	return reg.Entries[ref].FullXID
}

// NextDiagnosticTxLocked allocates the next TxRef diagnostic (virtual
// transaction) identity. Caller holds Engine.mu.
func (reg *Registry) NextDiagnosticTxLocked() (uint32, error) {
	if reg.diagnosticTx == ^uint32(0) {
		return 0, errors.New("stuntgres: transaction diagnostic identity exhausted")
	}
	reg.diagnosticTx++
	return reg.diagnosticTx, nil
}

// TransactionStatusLocked is pg_xact_status for a full XID: the status text,
// and false when the status is unknown. Caller holds Engine.mu.
func (reg *Registry) TransactionStatusLocked(xid uint64) (string, bool, error) {
	switch uint32(xid) {
	case 0:
		return "", false, nil
	case 1, 2:
		return "committed", true, nil
	}
	if xid > reg.assignedXID {
		return "", false, sqlError("22023", fmt.Sprintf("transaction ID %d is in the future", xid))
	}
	if xid < reg.transactionHorizon {
		return "", false, nil
	}
	r, ok := reg.History[xid]
	if !ok {
		return "", false, nil
	}
	if r.Ticket != nil && !r.Ticket.OwnershipEnded() && r.Ticket.AssignedFullXID() == xid {
		return "in progress", true, nil
	}
	if r.State == ids.TxCommitted {
		return "committed", true, nil
	}
	return "aborted", true, nil
}

// EndTransactionIdentityLocked records ref's in-progress assignment as
// committed or aborted and mirrors that onto its ticket. Caller holds Engine.mu.
func (reg *Registry) EndTransactionIdentityLocked(ref ids.TxRef, committed bool) {
	v := reg.Entries[ref]
	if v.FullXID == 0 {
		return
	}
	r, ok := reg.History[v.FullXID]
	if !ok || r.State != ids.TxInProgress {
		return
	}
	r.State = ids.TxAborted
	if committed {
		r.State = ids.TxCommitted
	}
	if r.Ticket != nil {
		r.Ticket.MirrorCompletion(r.State)
	}
	reg.History[v.FullXID] = r
	if v.FullXID > reg.latestCompletedXID {
		reg.latestCompletedXID = v.FullXID
	}
}
