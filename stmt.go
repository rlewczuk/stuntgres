package stuntgres

import (
	"context"
	"database/sql/driver"
	"errors"
	"stuntgres/internal/parser"
	"sync/atomic"
)

type stmt struct {
	conn   *conn
	ast    parser.Statement
	count  int
	closed atomic.Bool
}

func (s *stmt) Close() error  { s.closed.Store(true); return nil }
func (s *stmt) NumInput() int { return s.count }
func (s *stmt) check(ctx context.Context) error {
	if err := s.conn.check(ctx); err != nil {
		return err
	}
	if s.closed.Load() {
		return errors.New("stuntgres: statement is closed")
	}
	return nil
}
func (s *stmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	r, err := s.conn.execute(ctx, s.ast, args)
	if err != nil {
		return nil, err
	}
	return result(r.Count), nil
}
func (s *stmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	r, err := s.conn.execute(ctx, s.ast, args)
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
