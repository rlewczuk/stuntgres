package stuntgres

import (
	"context"
	"database/sql/driver"
	"errors"
	"runtime"
	"stuntgres/internal/engine"
	"stuntgres/internal/parser"
	"sync/atomic"
)

type conn struct {
	session       *engine.Session
	cleanup       runtime.Cleanup
	closed        atomic.Bool
	noticeHandler func(Error)
}

type cleanupCapsule struct{ session *engine.Session }

func cleanupSession(capsule cleanupCapsule) { capsule.session.RequestEnd(engine.Abandoned) }

func (c *conn) check(ctx context.Context) error {
	defer runtime.KeepAlive(c)
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.closed.Load() || !c.session.IsLive() {
		return errors.New("stuntgres: connection is closed")
	}
	return nil
}
func (c *conn) Close() error {
	defer runtime.KeepAlive(c)
	c.closed.Store(true)
	c.cleanup.Stop()
	c.session.RequestEnd(engine.CleanClose)
	return c.session.WaitEnd(context.Background())
}
func (c *conn) Ping(ctx context.Context) error {
	defer runtime.KeepAlive(c)
	op, err := c.session.Acquire(ctx)
	if err != nil {
		return err
	}
	defer op.Done()
	return op.Context().Err()
}
func (c *conn) ResetSession(ctx context.Context) error {
	defer runtime.KeepAlive(c)
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.session.Reuse(ctx); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		c.session.RequestEnd(engine.InvalidConnection)
		return driver.ErrBadConn
	}
	return nil
}
func (c *conn) IsValid() bool             { defer runtime.KeepAlive(c); return c.session.IsLive() }
func (c *conn) Begin() (driver.Tx, error) { return c.BeginTx(context.Background(), driver.TxOptions{}) }
func (c *conn) BeginTx(ctx context.Context, _ driver.TxOptions) (driver.Tx, error) {
	defer runtime.KeepAlive(c)
	if err := c.check(ctx); err != nil {
		return nil, err
	}
	return nil, &Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "explicit transactions are unsupported"}
}
func parseOne(query string) (parser.Statement, error) {
	ss, err := parser.Parse(query)
	if err != nil {
		return nil, err
	}
	if len(ss) > 1 {
		return nil, &Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "multiple statements are unsupported"}
	}
	if len(ss) == 0 {
		return nil, nil
	}
	return ss[0], nil
}
func (c *conn) Prepare(query string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), query)
}
func (c *conn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	defer runtime.KeepAlive(c)
	op, err := c.session.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer op.Done()
	if err := c.check(ctx); err != nil {
		return nil, err
	}
	s, err := parseOne(query)
	if err != nil {
		return nil, err
	}
	count := 0
	if s != nil {
		p, err := c.session.Engine().Describe(op, s)
		if err != nil {
			return nil, err
		}
		count = p.Count
	}
	if err := c.check(ctx); err != nil {
		return nil, err
	}
	return &stmt{conn: c, ast: s, count: count}, nil
}
func (c *conn) execute(ctx context.Context, s parser.Statement, args []driver.NamedValue) (engine.Result, error) {
	defer runtime.KeepAlive(c)
	op, err := c.session.Acquire(ctx)
	if err != nil {
		return engine.Result{}, err
	}
	defer op.Done()
	if err := c.check(ctx); err != nil {
		return engine.Result{}, err
	}
	if s == nil {
		if len(args) != 0 {
			return engine.Result{}, errors.New("stuntgres: expected zero arguments")
		}
		return engine.Result{}, nil
	}
	r, err := c.session.Engine().Execute(op, s, args)
	notices := op.DetachNotices()
	op.Done()
	if c.noticeHandler != nil {
		for _, notice := range notices {
			c.noticeHandler(notice)
		}
	}
	return r, err
}
func (c *conn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	defer runtime.KeepAlive(c)
	if err := c.check(ctx); err != nil {
		return nil, err
	}
	s, err := parseOne(q)
	if err != nil {
		return nil, err
	}
	r, err := c.execute(ctx, s, args)
	if err != nil {
		return nil, err
	}
	return result(r.Count), nil
}
func (c *conn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	defer runtime.KeepAlive(c)
	if err := c.check(ctx); err != nil {
		return nil, err
	}
	s, err := parseOne(q)
	if err != nil {
		return nil, err
	}
	r, err := c.execute(ctx, s, args)
	if err != nil {
		return nil, err
	}
	return newRows(ctx, r), nil
}
func named(values []driver.Value) []driver.NamedValue {
	out := make([]driver.NamedValue, len(values))
	for i, v := range values {
		out[i] = driver.NamedValue{Ordinal: i + 1, Value: v}
	}
	return out
}
func (c *conn) Exec(q string, args []driver.Value) (driver.Result, error) {
	return c.ExecContext(context.Background(), q, named(args))
}
func (c *conn) Query(q string, args []driver.Value) (driver.Rows, error) {
	return c.QueryContext(context.Background(), q, named(args))
}

var _ driver.ConnPrepareContext = (*conn)(nil)
var _ driver.ExecerContext = (*conn)(nil)
var _ driver.QueryerContext = (*conn)(nil)
var _ driver.ConnBeginTx = (*conn)(nil)
var _ driver.Pinger = (*conn)(nil)
var _ driver.SessionResetter = (*conn)(nil)
var _ driver.Validator = (*conn)(nil)
