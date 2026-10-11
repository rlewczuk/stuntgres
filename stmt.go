package stuntgres

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"runtime"
	"stuntgres/internal/engine"
	"stuntgres/internal/parser"
	"sync/atomic"
)

// Retains raw syntax, fixed signatures and private scalar captures, not a bound
// execution plan. Explicit DISCARD invalidates captures; ordinary queries rebind on use. FETCH resolves the live named
// cursor and publishes its actual schema, independently of the prepared hint.
type stmt struct {
	conn        *conn
	ast         parser.Statement
	source      string
	description engine.StatementDescription
	closed      atomic.Bool
}

func (s *stmt) Close() error  { defer runtime.KeepAlive(s.conn); s.closed.Store(true); return nil }
func (s *stmt) NumInput() int { defer runtime.KeepAlive(s.conn); return s.description.Parameters.Count }
func (s *stmt) check(ctx context.Context) error {
	defer runtime.KeepAlive(s.conn)
	if err := s.conn.check(ctx); err != nil {
		return err
	}
	if s.closed.Load() {
		return errors.New("stuntgres: statement is closed")
	}
	return nil
}
func (s *stmt) checkArguments(args []driver.NamedValue) error {
	if len(args) != s.description.Parameters.Count {
		return fmt.Errorf("stuntgres: got %d arguments, want %d", len(args), s.description.Parameters.Count)
	}
	return nil
}
func (s *stmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	defer runtime.KeepAlive(s.conn)
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	if err := s.checkArguments(args); err != nil {
		return nil, err
	}
	r, err := s.conn.execute(ctx, s.ast, args, &s.description)
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
func (s *stmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	defer runtime.KeepAlive(s.conn)
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	if err := s.checkArguments(args); err != nil {
		return nil, err
	}
	r, err := s.conn.execute(ctx, s.ast, args, &s.description)
	if err != nil {
		return nil, err
	}
	return newRows(ctx, r), nil
}
func (s *stmt) Exec(args []driver.Value) (driver.Result, error) {
	return s.ExecContext(context.Background(), named(args))
}
func (s *stmt) Query(args []driver.Value) (driver.Rows, error) {
	return s.QueryContext(context.Background(), named(args))
}

var _ driver.StmtExecContext = (*stmt)(nil)
var _ driver.StmtQueryContext = (*stmt)(nil)
