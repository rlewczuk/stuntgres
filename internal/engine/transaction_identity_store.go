package engine

import (
	"errors"
	"fmt"
)

type transactionIdentityState uint32

const (
	transactionInProgress transactionIdentityState = iota
	transactionCommitted
	transactionAborted
)

type transactionIdentityRecord struct {
	state  transactionIdentityState
	ticket *relationTicket
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
func multiTransactionAge(next, xid uint32) int32 {
	if xid == 0 {
		return 2147483647
	}
	return int32(next - xid)
}
func (e *Engine) transactionAgeLocked(ref TxRef, xid uint32) (int32, error) {
	if ref.owner != e || ref.epoch != e.state.epoch {
		return 0, ErrCatalogReplaced
	}
	v := e.visibility[ref]
	if !v.ageDemanded {
		anchor := v.assignedXID
		if anchor == 0 {
			next, err := nextFullTransactionID(e.assignedXID)
			if err != nil {
				return 0, err
			}
			anchor = uint32(next)
		}
		v.ageAnchor, v.ageDemanded = anchor, true
		if e.visibility == nil {
			e.visibility = make(map[TxRef]transactionVisibility)
		}
		e.visibility[ref] = v
	}
	return transactionAge(v.ageAnchor, xid), nil
}
func (e *Engine) transactionStatusLocked(xid uint64) (string, bool, error) {
	switch uint32(xid) {
	case 0:
		return "", false, nil
	case 1, 2:
		return "committed", true, nil
	}
	if xid > e.assignedXID {
		return "", false, sqlError("22023", fmt.Sprintf("transaction ID %d is in the future", xid))
	}
	if xid < e.transactionHorizon {
		return "", false, nil
	}
	r, ok := e.transactionHistory[xid]
	if !ok {
		return "", false, nil
	}
	if r.ticket != nil && !r.ticket.ownershipEnded() && r.ticket.assignedFullXID() == xid {
		return "in progress", true, nil
	}
	if r.state == transactionCommitted {
		return "committed", true, nil
	}
	return "aborted", true, nil
}
func (e *Engine) endTransactionIdentityLocked(ref TxRef, committed bool) {
	v := e.visibility[ref]
	if v.fullXID == 0 {
		return
	}
	r, ok := e.transactionHistory[v.fullXID]
	if !ok || r.state != transactionInProgress {
		return
	}
	r.state = transactionAborted
	if committed {
		r.state = transactionCommitted
	}
	if r.ticket != nil {
		r.ticket.mirrorCompletion(r.state)
	}
	e.transactionHistory[v.fullXID] = r
}
