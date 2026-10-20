package stuntgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"runtime"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"sync/atomic"
)

type conn struct {
	engine           *engine.Engine // The engine that hosts session; immutable.
	session          *session.Session
	cleanup          runtime.Cleanup
	closed           atomic.Bool
	terminalReported atomic.Bool
	noticeHandler    func(Error)
}

type cleanupCapsule struct{ session *session.Session }

func cleanupSession(capsule cleanupCapsule) { capsule.session.RequestEnd(session.Abandoned) }

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
	c.session.RequestEnd(session.CleanClose)
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
		c.session.RequestEnd(session.InvalidConnection)
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
	level := session.DefaultIsolation
	switch options.Isolation {
	case 0:
	case driver.IsolationLevel(sql.LevelReadCommitted):
		level = session.ReadCommitted
	case driver.IsolationLevel(sql.LevelReadUncommitted):
		level = session.ReadUncommitted
	case driver.IsolationLevel(sql.LevelRepeatableRead):
		level = session.RepeatableRead
	case driver.IsolationLevel(sql.LevelSerializable):
		level = session.Serializable
	default:
		return nil, errors.New("stuntgres: unsupported transaction isolation level")
	}
	op, err := c.session.AcquireActivity(ctx)
	if err != nil {
		return nil, c.terminalError(err)
	}
	defer op.Done()
	ref, err := c.engine.Begin(op, session.TxOptions{Isolation: level, ReadOnly: options.ReadOnly})
	if err != nil {
		return nil, c.terminalError(op.CommandError(err))
	}
	return &tx{conn: c, ref: ref}, nil
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

// Direct and prepared execution share one message/statement admission path.
// Completed outputs are transport, not transaction or execution authority.
type commandOutput struct {
	engine.Result
	sets []exec.RowResult
}

func (r *commandOutput) close() error {
	var first error
	if r.Rows != nil {
		first = r.Rows.Close()
		r.Rows = nil
	}
	for _, set := range r.sets {
		if err := set.Close(); first == nil {
			first = err
		}
	}
	r.sets = nil
	return first
}
func (c *conn) request(ctx context.Context, query string, ast parser.Statement, args []driver.NamedValue, parse, describe bool, expected *engine.StatementDescription) (r commandOutput, description engine.StatementDescription, parsed parser.Statement, err error) {
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
	scope, err := c.engine.StartStatement(op)
	if err != nil {
		err = c.terminalError(op.CommandError(err))
		return
	}
	batch := false
	defer func() {
		if batch {
			err = scope.FinishMessage(err)
		} else {
			err = scope.Finish(err)
		}
		err = c.terminalError(op.CommandError(err))
		if err != nil && !batch {
			r.close()
		}
		notices := op.DetachNotices()
		op.Done()
		if c.noticeHandler != nil {
			for _, notice := range notices {
				c.noticeHandler(notice)
			}
		}
	}()
	var statements []parser.Statement
	if parse {
		statements, err = parser.ParseWithOptions(query, options)
		if err != nil {
			return
		}
		if len(statements) > 1 && (describe || len(args) != 0) {
			err = &Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "cannot insert multiple commands into a prepared statement"}
			return
		}
		if len(statements) != 0 {
			ast = statements[0]
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
		return
	}
	if len(statements) > 1 {
		if err = scope.RetainMessage(); err != nil {
			return
		}
		batch = true
		for i, statement := range statements {
			if i != 0 {
				var next *engine.StatementScope
				next, err = scope.NextStatement()
				if err != nil {
					return
				}
				scope = next
			}
			var current engine.Result
			current, err = scope.Execute(statement, 0, frameImporter(values))
			err = scope.Finish(err)
			err = op.CommandError(err)
			if err != nil {
				if current.Rows != nil {
					current.Rows.Close()
				}
				for _, rows := range current.RowSets {
					rows.Close()
				}
				return
			}
			r.Count = current.Count
			if current.Rows != nil {
				r.sets = append(r.sets, current.Rows)
			}
			r.sets = append(r.sets, current.RowSets...)
		}
		return
	}
	scope.Expected = expected
	r.Result, err = scope.Execute(ast, len(values), frameImporter(values))
	r.sets = r.RowSets
	r.RowSets = nil
	return
}
func (c *conn) execute(ctx context.Context, ast parser.Statement, args []driver.NamedValue, expected *engine.StatementDescription) (engine.Result, error) {
	r, _, _, err := c.request(ctx, "", ast, args, false, false, expected)
	for _, rows := range r.sets {
		if closeErr := rows.Close(); err == nil {
			err = closeErr
		}
	}
	return r.Result, err
}
func (c *conn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	r, _, _, err := c.request(ctx, query, nil, args, true, false, nil)
	closeErr := r.close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return result(r.Count), nil
}
func (c *conn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	r, _, _, err := c.request(ctx, query, nil, args, true, false, nil)
	if len(r.sets) != 0 {
		return newBatchRows(ctx, r.sets, err), nil
	}
	if err != nil {
		return nil, err
	}
	return newRows(ctx, r.Result), nil
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
