package stuntgres

import (
	"stuntgres/internal/engine/core"
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"runtime"
	"sync/atomic"
)

// Retains raw syntax, fixed signatures, analysis identities and owned immutable folds,
// never an executable graph. Binding and authority are current on every use.
// FETCH and EXECUTE wrappers expose their live named resource's actual schema.
type stmt struct {
	conn        *conn
	definition core.PreparedDefinition
	closed      atomic.Bool
}

func (s *stmt) Close() error {
	defer runtime.KeepAlive(s.conn)
	if !s.closed.Swap(true) {
		s.conn.engine.RetirePreparedDescription(&s.definition.Description)
	}
	return nil
}
func (s *stmt) NumInput() int { defer runtime.KeepAlive(s.conn); return s.definition.Description.Parameters.Count }
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
	if len(args) != s.definition.Description.Parameters.Count {
		return fmt.Errorf("stuntgres: got %d arguments, want %d", len(args), s.definition.Description.Parameters.Count)
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
	r, err := s.conn.execute(ctx, s.definition.Source, s.definition.Body, args, &s.definition.Description)
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
	r, err := s.conn.execute(ctx, s.definition.Source, s.definition.Body, args, &s.definition.Description)
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
