package dual

import (
	"context"
	"database/sql/driver"
	"time"
)

// tx pairs a native transaction with its reference transaction (nil when the
// reference BeginTx failed). cancel ends the reference BeginTx context.
type tx struct {
	c      *conn
	native driver.Tx
	ref    driver.Tx
	cancel context.CancelFunc
}

type txOptions struct {
	Isolation string `json:"isolation"`
	ReadOnly  bool   `json:"read_only"`
}

func (t *tx) Commit() error {
	return t.end(CallCommit, t.native.Commit, func(rt driver.Tx) error { return rt.Commit() })
}

func (t *tx) Rollback() error {
	return t.end(CallRollback, t.native.Rollback, func(rt driver.Tx) error { return rt.Rollback() })
}

// end runs the native end, then the reference end (bounded), then releases
// the reference BeginTx context.
func (t *tx) end(call Call, native func() error, ref func(driver.Tx) error) error {
	start := time.Now()
	err := native()
	e := t.c.open(call, start, "", nil)
	e.nerr = err
	missing := ""
	if t.ref == nil {
		missing = "no reference transaction"
	}
	rt := t.ref
	mirror(e, context.Background(), missing, false, func(context.Context, driver.Conn) (struct{}, error) {
		return struct{}{}, ref(rt)
	})
	t.ref = nil
	if t.cancel != nil {
		t.cancel()
		t.cancel = nil
	}
	t.c.txCancel = nil
	e.finish()
	return err
}
