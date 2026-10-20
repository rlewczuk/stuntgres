package engine

import (
	"fmt"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"time"
)

var _ functions.TransactionInquiryRuntime = (*EvalContext)(nil)

func (c *EvalContext) CheckTransactionInquiryContext() error { return evaluationContext(c) }
func (c *EvalContext) RequireTransactionIdentityScope() error {
	if c.liveHost() == nil {
		return fmt.Errorf("stuntgres: transaction identity lacks statement scope")
	}
	return nil
}
func (c *EvalContext) TransactionAge(xid uint32) (int32, error) {
	host := c.liveHost()
	if host == nil {
		return 0, fmt.Errorf("stuntgres: transaction identity lacks statement scope")
	}
	return host.TransactionAge(xid)
}

func (h *statementHost) TransactionAge(xid uint32) (int32, error) {
	scope := h.scope()
	e := scope.op.session.Engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	if scope.txRef.Epoch() != e.catalog.state.Epoch() {
		return 0, ids.ErrCatalogReplaced
	}
	return e.transactionAgeLocked(scope.txRef, xid)
}
func (c *EvalContext) MultiTransactionAge(xid uint32) (int32, error) {
	host := c.liveHost()
	if host == nil {
		return 0, fmt.Errorf("stuntgres: transaction identity lacks statement scope")
	}
	return host.MultiTransactionAge(xid)
}

func (h *statementHost) MultiTransactionAge(xid uint32) (int32, error) {
	scope := h.scope()
	return scope.op.session.Engine().locks.MultiAge(scope.ticket, xid)
}
func (c *EvalContext) CurrentTransactionID(assign bool) (uint64, error) {
	host := c.liveHost()
	if host == nil {
		return 0, fmt.Errorf("stuntgres: transaction identity lacks statement scope")
	}
	return host.CurrentTransactionID(assign)
}

func (h *statementHost) CurrentTransactionID(assign bool) (uint64, error) {
	scope := h.scope()
	e := scope.op.session.Engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	if scope.txRef.Epoch() != e.catalog.state.Epoch() {
		return 0, ids.ErrCatalogReplaced
	}
	if assign {
		if err := e.assignTransactionXIDLocked(scope.txRef); err != nil {
			return 0, err
		}
	}
	return e.visibility.FullXIDLocked(scope.txRef), nil
}
func (c *EvalContext) TransactionStatus(full uint64) (string, bool, error) {
	host := c.liveHost()
	if host == nil {
		return "", false, fmt.Errorf("stuntgres: transaction identity lacks statement scope")
	}
	return host.TransactionStatus(full)
}

func (h *statementHost) TransactionStatus(full uint64) (string, bool, error) {
	scope := h.scope()
	e := scope.op.session.Engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	if scope.txRef.Epoch() != e.catalog.state.Epoch() {
		return "", false, ids.ErrCatalogReplaced
	}
	return e.visibility.TransactionStatusLocked(full)
}
func (c *EvalContext) TransactionMembers(xid uint32, result *sqltype.Descriptor) ([]sqltype.Value, error) {
	host := c.liveHost()
	if host == nil {
		return nil, fmt.Errorf("stuntgres: MultiXact lacks statement scope")
	}
	return host.TransactionMembers(xid, result)
}

func (h *statementHost) TransactionMembers(xid uint32, result *sqltype.Descriptor) ([]sqltype.Value, error) {
	scope := h.scope()
	members, err := scope.op.session.Engine().locks.MultiXactMembers(scope.ticket, xid)
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
func (c *EvalContext) CommitTimestamp(xid uint32) (time.Time, uint32, bool, error) {
	host := c.liveHost()
	if host == nil {
		return time.Time{}, 0, false, fmt.Errorf("stuntgres: missing commit timestamp scope")
	}
	return host.CommitTimestamp(xid)
}

func (h *statementHost) CommitTimestamp(xid uint32) (time.Time, uint32, bool, error) {
	scope := h.scope()
	e := scope.op.session.Engine()
	e.mu.Lock()
	if !e.visibility.TracksCommitTimestamps() {
		e.mu.Unlock()
		return time.Time{}, 0, false, commitTimestampDisabled()
	}
	entry := e.visibility.LookupCommitTimestampLocked(xid)
	e.mu.Unlock()
	return entry.Stamp, entry.Origin, entry.Present, nil
}
func (c *EvalContext) LastCommittedTransaction() (uint32, time.Time, uint32, bool, error) {
	host := c.liveHost()
	if host == nil {
		return 0, time.Time{}, 0, false, fmt.Errorf("stuntgres: missing commit timestamp scope")
	}
	return host.LastCommittedTransaction()
}

func (h *statementHost) LastCommittedTransaction() (uint32, time.Time, uint32, bool, error) {
	scope := h.scope()
	e := scope.op.session.Engine()
	e.mu.Lock()
	if !e.visibility.TracksCommitTimestamps() {
		e.mu.Unlock()
		return 0, time.Time{}, 0, false, commitTimestampDisabled()
	}
	xid, entry := e.visibility.LastCommittedXID(), e.visibility.LastCommitTimestamp()
	e.mu.Unlock()
	return xid, entry.Stamp, entry.Origin, entry.Present, nil
}
