package stuntgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"runtime"
	"stuntgres/internal/engine"
	"stuntgres/internal/parser"
	"sync/atomic"
)

type conn struct {
	session          *engine.Session
	cleanup          runtime.Cleanup
	closed           atomic.Bool
	terminalReported atomic.Bool
	noticeHandler    func(Error)
}

type cleanupCapsule struct{ session *engine.Session }

func cleanupSession(capsule cleanupCapsule) { capsule.session.RequestEnd(engine.Abandoned) }

func (c *conn) check(ctx context.Context) error {
	defer runtime.KeepAlive(c)
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.terminalReported.Load() {
		return driver.ErrBadConn
	}
	if c.closed.Load() {
		return errors.New("stuntgres: connection is closed")
	}
	return c.terminalError(c.session.Check(ctx))
}

// An accepted fatal is never ErrBadConn (database/sql could replay it). Only
// subsequent pre-admission use of this already-reported dead owner gets that
// sentinel; pool Validator/ResetSession can discard it without any SQL replay.
func (c *conn) terminalError(err error) error {
	var diagnostic *Error
	if errors.As(err, &diagnostic) && diagnostic.Severity == "FATAL" {
		c.terminalReported.Store(true)
	}
	return err
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
	if err := c.check(ctx); err != nil {
		return err
	}
	op, err := c.session.AcquireActivity(ctx)
	if err != nil {
		return c.terminalError(err)
	}
	defer op.Done()
	return c.terminalError(op.CommandError(op.Context().Err()))
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
func (c *conn) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	defer runtime.KeepAlive(c)
	if err := c.check(ctx); err != nil {
		return nil, err
	}
	level := engine.DefaultIsolation
	switch options.Isolation {
	case 0:
	case driver.IsolationLevel(sql.LevelReadCommitted):
		level = engine.ReadCommitted
	case driver.IsolationLevel(sql.LevelReadUncommitted):
		level = engine.ReadUncommitted
	case driver.IsolationLevel(sql.LevelRepeatableRead):
		level = engine.RepeatableRead
	case driver.IsolationLevel(sql.LevelSerializable):
		level = engine.Serializable
	default:
		return nil, errors.New("stuntgres: unsupported transaction isolation level")
	}
	op, err := c.session.AcquireActivity(ctx)
	if err != nil {
		return nil, c.terminalError(err)
	}
	defer op.Done()
	ref, err := c.session.Engine().Begin(op, engine.TxOptions{Isolation: level, ReadOnly: options.ReadOnly})
	if err != nil {
		return nil, c.terminalError(op.CommandError(err))
	}
	return &tx{conn: c, ref: ref}, nil
}
func parseOne(query string, options parser.Options) (parser.Statement, error) {
	ss, err := parser.ParseWithOptions(query, options)
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
	_, description, ast, err := c.request(ctx, query, nil, nil, true, true, nil)
	if err != nil {
		return nil, err
	}
	return &stmt{conn: c, ast: ast, source: query, description: description}, nil
}

// Direct parsing and prepared execution share one admitted completion boundary.
// A prepared handle retains only its AST/description, never a visibility snapshot.
func (c *conn) request(ctx context.Context, query string, ast parser.Statement, args []driver.NamedValue, parse, describe bool, expected *engine.StatementDescription) (r engine.Result, description engine.StatementDescription, parsed parser.Statement, err error) {
	defer runtime.KeepAlive(c)
	if err = c.check(ctx); err != nil {
		return
	}
	values, err := normalizeArguments(args)
	if err != nil {
		return
	}
	op, options, err := c.session.AcquireCommand(ctx)
	if err != nil {
		err = c.terminalError(err)
		return
	}
	defer op.Done()
	scope, err := c.session.Engine().StartStatement(op)
	if err != nil {
		err = c.terminalError(op.CommandError(err))
		return
	}
	defer func() {
		err = scope.Finish(err)
		err = c.terminalError(op.CommandError(err))
		if err != nil {
			if r.Rows != nil {
				r.Rows.Close()
			}
			r = engine.Result{}
		}
		notices := op.DetachNotices()
		op.Done()
		if c.noticeHandler != nil {
			for _, notice := range notices {
				c.noticeHandler(notice)
			}
		}
	}()
	if parse {
		ast, err = parseOne(query, options)
		if err != nil {
			return
		}
	}
	parsed = ast
	if ast == nil {
		if len(args) != 0 {
			err = errors.New("stuntgres: expected zero arguments")
		}
		return
	}
	if describe {
		description, err = scope.Describe(ast)
	} else {
		// Signature and immutable timestamp scalars travel together; execution
		// still resolves the current catalog and imports a fresh parameter frame.
		scope.Expected = expected
		r, err = scope.Execute(ast, len(values), frameImporter(values))
	}
	return
}
func (c *conn) execute(ctx context.Context, ast parser.Statement, args []driver.NamedValue, expected *engine.StatementDescription) (engine.Result, error) {
	r, _, _, err := c.request(ctx, "", ast, args, false, false, expected)
	return r, err
}
func (c *conn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	r, _, _, err := c.request(ctx, query, nil, args, true, false, nil)
	if err != nil {
		return nil, err
	}
	if r.Rows != nil {
		if err := r.Rows.Close(); err != nil {
			return nil, err
		}
	}
	return result(r.Count), nil
}
func (c *conn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	r, _, _, err := c.request(ctx, query, nil, args, true, false, nil)
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
