package dual

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"time"
)

// conn is one dual connection: a native connection and its paired reference.
// database/sql serializes every call on one driver.Conn (and on its Stmt, Tx
// and Rows) under its driverConn lock, so the per-connection state needs no
// mutex. Abandoned reference tasks never touch it.
type conn struct {
	cn       *connector
	native   driver.Conn
	ref      pairRef
	id       uint64
	cycles   uint64
	out      emitter
	txCancel context.CancelFunc // reference BeginTx context of the open transaction
}

// open starts the record of a call that began at start and whose native part
// has just returned; it assigns the call's cycle.
func (c *conn) open(call Call, start time.Time, query string, args []driver.NamedValue) *entry {
	c.cycles++
	return &entry{c: c, start: start, end: time.Now(), rec: record{
		Tstamp:       start.UTC().Format(time.RFC3339Nano),
		ConnectionID: c.id,
		Cycle:        c.cycles,
		Call:         call,
		Query:        query,
		Params:       renderParams(args),
	}}
}

func (c *conn) Prepare(query string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), query)
}

// prepared is a reference statement and its parameter count.
type prepared struct {
	stmt     driver.Stmt
	numInput int
}

func (c *conn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	start := time.Now()
	ns, err := ctxPrepare(ctx, c.native, query)
	e := c.open(CallPrepare, start, query, nil)
	e.rec.Statement = e.rec.Cycle
	e.nerr = err
	failed := err != nil
	out, _ := mirror(e, ctx, "", false, func(ctx context.Context, rc driver.Conn) (prepared, error) {
		st, err := ctxPrepare(ctx, rc, query)
		if err != nil {
			return prepared{}, err
		}
		p := prepared{stmt: st, numInput: st.NumInput()}
		if failed {
			_ = st.Close()
			p.stmt = nil
		}
		return p, nil
	})
	refOK := out.failure == "" && out.err == nil && e.ran
	if refOK {
		e.rec.ResponseRef = prepSummary{NumInput: out.value.numInput}
	}
	if err != nil {
		e.finish()
		return nil, err
	}
	s := &stmt{c: c, native: ns, query: query, id: e.rec.Cycle}
	if refOK {
		s.ref = out.value.stmt
	}
	e.summary = func() { e.rec.Response = prepSummary{NumInput: ns.NumInput()} }
	e.compare = func(d *diffLines) bool {
		if r, ok := e.rec.ResponseRef.(prepSummary); ok {
			comparePrepare(d, e.rec.Response.(prepSummary), r)
		}
		return false
	}
	e.finish()
	return wrapStmt(s), nil
}

func (c *conn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	start := time.Now()
	res, err := ctxExec(ctx, c.native, query, args)
	if err == driver.ErrSkip {
		return res, err
	}
	e := c.open(CallExec, start, query, args)
	e.nerr = err
	out, _ := mirror(e, ctx, "", false, func(ctx context.Context, rc driver.Conn) (any, error) {
		r, err := refExec(ctx, rc, query, args)
		return summarizeResult(r, err), err
	})
	e.execResult(res, err, out)
	e.finish()
	return res, err
}

func (c *conn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	start := time.Now()
	nr, err := ctxQuery(ctx, c.native, query, args)
	if err == driver.ErrSkip {
		return nr, err
	}
	e := c.open(CallQuery, start, query, args)
	return c.queryResult(ctx, e, nr, err, "", func(ctx context.Context, rc driver.Conn) (*rowsOutcome, error) {
		return refQuery(ctx, rc, query, args)
	})
}

// queryResult mirrors a query whose native part returned nr, err. The
// reference rows are drained inside the bounded task; the record is written
// now when the native query failed, else when the caller closes the rows.
func (c *conn) queryResult(ctx context.Context, e *entry, nr driver.Rows, err error, missing string, call func(context.Context, driver.Conn) (*rowsOutcome, error)) (driver.Rows, error) {
	e.nerr = err
	out, _ := mirror(e, ctx, missing, false, call)
	ref := out.value
	if out.failure != "" {
		ref = nil
	}
	if err != nil || nr == nil {
		if ref != nil {
			e.rec.ResponseRef = summarizeRows(ref)
		}
		e.finish()
		return nr, err
	}
	return newRows(e, nr, ref), nil
}

func (c *conn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

func (c *conn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	start := time.Now()
	nt, err := ctxBegin(ctx, c.native, opts)
	e := c.open(CallBegin, start, "", nil)
	e.rec.TxOptions = &txOptions{Isolation: sql.IsolationLevel(opts.Isolation).String(), ReadOnly: opts.ReadOnly}
	e.nerr = err
	failed := err != nil
	out, _ := mirror(e, ctx, "", !failed, func(ctx context.Context, rc driver.Conn) (driver.Tx, error) {
		rt, err := ctxBegin(ctx, rc, opts)
		if err == nil && failed {
			_ = rt.Rollback()
			return nil, nil
		}
		return rt, err
	})
	e.finish()
	if out.value == nil && out.cancel != nil {
		out.cancel()
		out.cancel = nil
	}
	if err != nil {
		return nil, err
	}
	c.txCancel = out.cancel
	return &tx{c: c, native: nt, ref: out.value, cancel: out.cancel}, nil
}

func (c *conn) Ping(ctx context.Context) error {
	start := time.Now()
	var err error
	if p, ok := c.native.(driver.Pinger); ok {
		err = p.Ping(ctx)
	}
	e := c.open(CallPing, start, "", nil)
	e.nerr = err
	mirror(e, ctx, "", false, func(ctx context.Context, rc driver.Conn) (struct{}, error) {
		return struct{}{}, refPing(ctx, rc)
	})
	e.finish()
	return err
}

// resetSession backs the SessionResetter variants. The native result is
// always returned; a reference that fails to reset makes the pair lost.
func (c *conn) resetSession(ctx context.Context) error {
	start := time.Now()
	err := c.native.(driver.SessionResetter).ResetSession(ctx)
	e := c.open(CallResetSession, start, "", nil)
	e.nerr = err
	out, ran := mirror(e, ctx, "", false, func(ctx context.Context, rc driver.Conn) (struct{}, error) {
		return struct{}{}, refReset(ctx, rc)
	})
	if ran && out.failure == "" && out.err != nil {
		e.failure = "reference connection lost: " + out.err.Error()
		c.ref.lose(e.failure, true)
	}
	e.finish()
	return err
}

// isValid backs the Validator variants. The native result is always returned;
// an invalid reference makes the pair lost.
func (c *conn) isValid() bool {
	start := time.Now()
	valid := c.native.(driver.Validator).IsValid()
	e := c.open(CallIsValid, start, "", nil)
	e.rec.Response = validSummary{Valid: valid}
	out, ran := mirror(e, context.Background(), "", false, func(_ context.Context, rc driver.Conn) (bool, error) {
		return refValid(rc), nil
	})
	if ran && out.failure == "" {
		e.rec.ResponseRef = validSummary{Valid: out.value}
		if !out.value {
			e.failure = "reference connection lost: connection is no longer valid"
			c.ref.lose(e.failure, true)
		}
	}
	e.finish()
	return valid
}

// Close closes the native connection, then the healthy reference connection
// (bounded). A lost pair's connection is closed by its owner. Waiting records
// are flushed in cycle order.
func (c *conn) Close() error {
	start := time.Now()
	err := c.native.Close()
	e := c.open(CallClose, start, "", nil)
	e.nerr = err
	if reason := c.skip(context.Background(), nil); reason != "" {
		e.rec.Unmirrored = reason
	} else {
		rc := c.ref.conn
		timeout := c.cn.opts.ReferenceTimeout
		t := runTask(context.Background(), timeout, false, func(context.Context) (struct{}, error) {
			return struct{}{}, rc.Close()
		}, nil)
		e.ran, e.rerr, e.refDur = true, t.err, t.duration
		switch {
		case t.abandoned:
			e.failure = "reference did not respond within " + timeout.String() + "; connection closed"
		case t.panicked != nil:
			e.failure = fmt.Sprintf("reference connection lost: panic: %v", t.panicked)
		}
		c.ref.conn = nil
		c.ref.code, c.ref.message = codeLost, "reference connection closed"
	}
	if c.txCancel != nil {
		c.txCancel()
		c.txCancel = nil
	}
	e.finish()
	c.out.flush()
	return err
}
