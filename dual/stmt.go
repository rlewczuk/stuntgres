package dual

import (
	"context"
	"database/sql/driver"
	"time"
)

// stmt pairs a native statement with its reference statement on the paired
// reference connection (nil when the reference Prepare failed). id is the
// cycle of the prepare record.
type stmt struct {
	c      *conn
	native driver.Stmt
	ref    driver.Stmt
	query  string
	id     uint64
}

func (s *stmt) missing() string {
	if s.ref == nil {
		return "no reference statement"
	}
	return ""
}

// NumInput is the native value; the reference count was compared at prepare.
func (s *stmt) NumInput() int { return s.native.NumInput() }

func (s *stmt) Close() error {
	start := time.Now()
	err := s.native.Close()
	e := s.c.open(CallStmtClose, start, s.query, nil)
	e.rec.Statement = s.id
	e.nerr = err
	ref := s.ref
	mirror(e, context.Background(), s.missing(), false, func(context.Context, driver.Conn) (struct{}, error) {
		return struct{}{}, ref.Close()
	})
	s.ref = nil
	e.finish()
	return err
}

func (s *stmt) Exec(args []driver.Value) (driver.Result, error) {
	return s.ExecContext(context.Background(), namedArgs(args))
}

func (s *stmt) Query(args []driver.Value) (driver.Rows, error) {
	return s.QueryContext(context.Background(), namedArgs(args))
}

func (s *stmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	start := time.Now()
	res, err := ctxStmtExec(ctx, s.native, args)
	if err == driver.ErrSkip {
		return res, err
	}
	e := s.c.open(CallStmtExec, start, s.query, args)
	e.rec.Statement = s.id
	e.nerr = err
	ref := s.ref
	out, _ := mirror(e, ctx, s.missing(), false, func(ctx context.Context, _ driver.Conn) (any, error) {
		r, err := ctxStmtExec(ctx, ref, args)
		return summarizeResult(r, err), err
	})
	e.execResult(res, err, out)
	e.finish()
	return res, err
}

func (s *stmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	start := time.Now()
	nr, err := ctxStmtQuery(ctx, s.native, args)
	if err == driver.ErrSkip {
		return nr, err
	}
	e := s.c.open(CallStmtQuery, start, s.query, args)
	e.rec.Statement = s.id
	ref := s.ref
	return s.c.queryResult(ctx, e, nr, err, s.missing(), func(ctx context.Context, _ driver.Conn) (*rowsOutcome, error) {
		return refStmtQuery(ctx, ref, args)
	})
}
