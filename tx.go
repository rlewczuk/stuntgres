package stuntgres

import (
	"context"
	"database/sql/driver"
	"errors"
	"runtime"
	"stuntgres/internal/engine"
	"sync/atomic"
)

// tx is incarnation-qualified, not the connection itself. End operations use a
// fresh context: database/sql cancels its BeginTx context before driver Commit.
type tx struct {
	conn *conn
	ref  engine.TxRef
	done atomic.Bool
}

func (t *tx) end(commit bool) error {
	defer runtime.KeepAlive(t.conn)
	if !t.done.CompareAndSwap(false, true) {
		return errors.New("stuntgres: transaction is done")
	}
	if err := t.conn.check(context.Background()); err != nil {
		return err
	}
	op, err := t.conn.session.AcquireActivity(context.Background())
	if err != nil {
		return t.conn.terminalError(err)
	}
	defer op.Done()
	if !commit {
		_, err = t.conn.session.Engine().Rollback(op, t.ref)
		return t.conn.terminalError(op.CommandError(err))
	}
	result, err := t.conn.session.Engine().Commit(op, t.ref)
	if err != nil {
		return t.conn.terminalError(op.CommandError(err))
	}
	if !result.Committed {
		return errors.New("pq: could not complete operation in a failed transaction")
	}
	return nil
}

func (t *tx) Commit() error   { return t.end(true) }
func (t *tx) Rollback() error { return t.end(false) }

var _ driver.Tx = (*tx)(nil)
