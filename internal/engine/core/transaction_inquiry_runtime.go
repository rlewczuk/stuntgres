package core

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"time"
)

func (h *statementHost) TransactionAge(xid uint32) (int32, error) {
	scope := h.scope()
	e := scope.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if scope.TxRef.Epoch() != e.Catalog.State.Epoch() {
		return 0, ids.ErrCatalogReplaced
	}
	return e.transactionAgeLocked(scope.TxRef, xid)
}

func (h *statementHost) MultiTransactionAge(xid uint32) (int32, error) {
	scope := h.scope()
	return scope.Engine().Locks.MultiAge(scope.LockTicket, xid)
}

func (h *statementHost) CurrentTransactionID(assign bool) (uint64, error) {
	scope := h.scope()
	e := scope.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if scope.TxRef.Epoch() != e.Catalog.State.Epoch() {
		return 0, ids.ErrCatalogReplaced
	}
	if assign {
		if err := e.AssignTransactionXIDLocked(scope.TxRef); err != nil {
			return 0, err
		}
	}
	return e.Visibility.FullXIDLocked(scope.TxRef), nil
}

func (h *statementHost) CurrentSnapshot() (*sqltype.Snapshot, error) {
	scope := h.scope()
	e := scope.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	scope.Op.Session.Mu.Lock()
	defer scope.Op.Session.Mu.Unlock()
	if err := PublicationOperationLocked(e, scope.Op); err != nil {
		return nil, err
	}
	if scope.TxRef.Epoch() != e.Catalog.State.Epoch() {
		return nil, ids.ErrCatalogReplaced
	}
	if scope.currentSnapshot == nil {
		return nil, SQLError("55000", "no active snapshot")
	}
	return scope.currentSnapshot, nil
}

func (h *statementHost) TransactionStatus(full uint64) (string, bool, error) {
	scope := h.scope()
	e := scope.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if scope.TxRef.Epoch() != e.Catalog.State.Epoch() {
		return "", false, ids.ErrCatalogReplaced
	}
	return e.Visibility.TransactionStatusLocked(full)
}

func (h *statementHost) TransactionMembers(xid uint32, result *sqltype.Descriptor) ([]sqltype.Value, error) {
	scope := h.scope()
	members, err := scope.Engine().Locks.MultiXactMembers(scope.Engine().Catalog.Domain, scope.LockTicket, xid)
	if err != nil {
		return nil, err
	}
	// The manager's sole snapshot clone is released before SQL packing. No
	// engine mutex or manager mutex is held while constructing owned records.
	rows := make([]sqltype.Value, len(members))
	for i, m := range members {
		x, err := sqltype.XIDValue(result.CompositeMember(0).Usage.Type, m.XID)
		if err != nil {
			return nil, err
		}
		mode := sqltype.String(result.CompositeMember(1).Usage.Type, m.Mode.Name())
		rows[i], err = sqltype.OwnedCompositeValue(result, []sqltype.Value{x, mode})
		if err != nil {
			return nil, err
		}
	}
	return rows, nil
}
func commitTimestampDisabled() error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: "could not get commit timestamp data", Hint: `Make sure the configuration parameter "track_commit_timestamp" is set.`}
}

func (h *statementHost) CommitTimestamp(xid uint32) (time.Time, uint32, bool, error) {
	scope := h.scope()
	e := scope.Engine()
	e.Mu.Lock()
	if !e.Visibility.TracksCommitTimestamps() {
		e.Mu.Unlock()
		return time.Time{}, 0, false, commitTimestampDisabled()
	}
	entry := e.Visibility.LookupCommitTimestampLocked(xid)
	e.Mu.Unlock()
	return entry.Stamp, entry.Origin, entry.Present, nil
}

func (h *statementHost) LastCommittedTransaction() (uint32, time.Time, uint32, bool, error) {
	scope := h.scope()
	e := scope.Engine()
	e.Mu.Lock()
	if !e.Visibility.TracksCommitTimestamps() {
		e.Mu.Unlock()
		return 0, time.Time{}, 0, false, commitTimestampDisabled()
	}
	xid, entry := e.Visibility.LastCommittedXID(), e.Visibility.LastCommitTimestamp()
	e.Mu.Unlock()
	return xid, entry.Stamp, entry.Origin, entry.Present, nil
}
