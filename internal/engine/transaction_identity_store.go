package engine

import (
	"errors"
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/sqlstate"
)

type transactionIdentityRecord struct {
	State  ids.TxCompletion
	Ticket *lock.Ticket
}

func nextFullTransactionID(last uint64) (uint64, error) {
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
func transactionAge(now, xid uint32) int32 {
	if xid < 3 {
		return 2147483647
	}
	return int32(now - xid)
}

// transactionAgeLocked fences ref against the current catalog epoch.
func (e *Engine) transactionAgeLocked(ref ids.TxRef, xid uint32) (int32, error) {
	if ref.Domain() != domainOf(e) || ref.Epoch() != e.catalog.state.Epoch() {
		return 0, ids.ErrCatalogReplaced
	}
	return e.visibility.TransactionAgeLocked(ref, xid)
}

// transactionAgeLocked anchors ref's age() once, at its assigned XID or the
// next one to be assigned. Caller holds Engine.mu and has fenced ref.
func (reg *visibilityRegistry) TransactionAgeLocked(ref ids.TxRef, xid uint32) (int32, error) {
	v := reg.Entries[ref]
	if !v.AgeDemanded {
		anchor := v.AssignedXID
		if anchor == 0 {
			next, err := nextFullTransactionID(reg.assignedXID)
			if err != nil {
				return 0, err
			}
			anchor = uint32(next)
		}
		v.AgeAnchor, v.AgeDemanded = anchor, true
		if reg.Entries == nil {
			reg.Entries = make(map[ids.TxRef]transactionVisibility)
		}
		reg.Entries[ref] = v
	}
	return transactionAge(v.AgeAnchor, xid), nil
}

// assignedXIDLocked is ref's public low-32 XID, zero when unassigned.
// Caller holds Engine.mu.
func (reg *visibilityRegistry) AssignedXIDLocked(ref ids.TxRef) uint32 {
	return reg.Entries[ref].AssignedXID
}

// fullXIDLocked is ref's canonical full XID, zero when unassigned. Caller
// holds Engine.mu.
func (reg *visibilityRegistry) FullXIDLocked(ref ids.TxRef) uint64 {
	return reg.Entries[ref].FullXID
}

// nextDiagnosticTxLocked allocates the next TxRef diagnostic (virtual
// transaction) identity. Caller holds Engine.mu.
func (reg *visibilityRegistry) NextDiagnosticTxLocked() (uint32, error) {
	if reg.diagnosticTx == ^uint32(0) {
		return 0, errors.New("stuntgres: transaction diagnostic identity exhausted")
	}
	reg.diagnosticTx++
	return reg.diagnosticTx, nil
}

func (reg *visibilityRegistry) TransactionStatusLocked(xid uint64) (string, bool, error) {
	switch uint32(xid) {
	case 0:
		return "", false, nil
	case 1, 2:
		return "committed", true, nil
	}
	if xid > reg.assignedXID {
		return "", false, registrySQLError("22023", fmt.Sprintf("transaction ID %d is in the future", xid))
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
func (reg *visibilityRegistry) EndTransactionIdentityLocked(ref ids.TxRef, committed bool) {
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
}

// registrySQLError builds the registry's own SQLSTATE errors, matching the
// engine's sqlError.
func registrySQLError(code, message string) error {
	return &sqlstate.Error{
		Severity: "ERROR", SeverityUnlocalized: "ERROR",
		Code: code, Message: message,
	}
}
