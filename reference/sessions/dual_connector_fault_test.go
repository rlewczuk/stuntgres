package reference_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"sync"
	"testing"
)

// dualFault wraps a base reference connector (native in quick mode, lib/pq
// with REFERENCE=1) with deterministic faults, so fault scenarios run against
// a real reference underneath in both modes. Knobs are set before the call
// they affect; the mutex guards them and the counters.
type dualFault struct {
	base driver.Connector

	mu           sync.Mutex
	connectErr   error                    // Connect fails without dialing
	stallConnect chan struct{}            // Connect blocks (ignoring ctx) until closed
	predialed    driver.Conn              // handed out by the next Connect
	stall        map[string]chan struct{} // query blocks (ignoring ctx) until closed, then runs
	inject       map[string]error         // query fails without executing
	result       map[string]driver.Result // Exec runs, then returns this result
	resetErr     error                    // ResetSession fails
	conns        []*dualFaultConn
	open, rows   int
}

func newDualFault() *dualFault {
	return &dualFault{stall: map[string]chan struct{}{}, inject: map[string]error{}, result: map[string]driver.Result{}}
}

// wrap is newDualPair's wrap argument: it adopts the scenario's reference.
func (f *dualFault) wrap(base driver.Connector) driver.Connector {
	f.base = base
	return f
}

// predial opens the base connection now, so short-timeout tests do not time
// the reference Connect.
func (f *dualFault) predial(t *testing.T, ctx context.Context) {
	t.Helper()
	c, err := f.base.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.predialed = c
	f.mu.Unlock()
}

func (f *dualFault) Driver() driver.Driver { return f.base.Driver() }

func (f *dualFault) Connect(ctx context.Context) (driver.Conn, error) {
	f.mu.Lock()
	err, stall, base := f.connectErr, f.stallConnect, f.predialed
	f.predialed = nil
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if stall != nil {
		<-stall
	}
	if base == nil {
		if base, err = f.base.Connect(ctx); err != nil {
			return nil, err
		}
	}
	c := &dualFaultConn{f: f, base: base}
	f.mu.Lock()
	f.conns = append(f.conns, c)
	f.open++
	f.mu.Unlock()
	return c, nil
}

func (f *dualFault) openConns() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.open
}

func (f *dualFault) openRows() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rows
}

// kill really closes the newest base connection; later base errors flow as-is.
func (f *dualFault) kill(t *testing.T) {
	t.Helper()
	f.mu.Lock()
	c := f.conns[len(f.conns)-1]
	f.mu.Unlock()
	if err := c.base.Close(); err != nil {
		t.Fatalf("kill: %v", err)
	}
}

// before applies the stall and inject knobs of query.
func (f *dualFault) before(query string) error {
	f.mu.Lock()
	stall, err := f.stall[query], f.inject[query]
	f.mu.Unlock()
	if stall != nil {
		<-stall
	}
	return err
}

type dualFaultConn struct {
	f      *dualFault
	base   driver.Conn
	closed bool
}

func (c *dualFaultConn) Prepare(query string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), query)
}

func (c *dualFaultConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if err := c.f.before(query); err != nil {
		return nil, err
	}
	s, err := c.base.(driver.ConnPrepareContext).PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	return &dualFaultStmt{f: c.f, base: s}, nil
}

func (c *dualFaultConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if err := c.f.before(query); err != nil {
		return nil, err
	}
	res, err := c.base.(driver.ExecerContext).ExecContext(ctx, query, args)
	c.f.mu.Lock()
	override := c.f.result[query]
	c.f.mu.Unlock()
	if err == nil && override != nil {
		return override, nil
	}
	return res, err
}

func (c *dualFaultConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if err := c.f.before(query); err != nil {
		return nil, err
	}
	r, err := c.base.(driver.QueryerContext).QueryContext(ctx, query, args)
	if err != nil {
		return nil, err
	}
	return c.f.track(r), nil
}

func (c *dualFaultConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

func (c *dualFaultConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return c.base.(driver.ConnBeginTx).BeginTx(ctx, opts)
}

func (c *dualFaultConn) Ping(ctx context.Context) error { return c.base.(driver.Pinger).Ping(ctx) }

func (c *dualFaultConn) ResetSession(ctx context.Context) error {
	c.f.mu.Lock()
	err := c.f.resetErr
	c.f.mu.Unlock()
	if err != nil {
		return err
	}
	return c.base.(driver.SessionResetter).ResetSession(ctx)
}

func (c *dualFaultConn) IsValid() bool { return c.base.(driver.Validator).IsValid() }

func (c *dualFaultConn) Close() error {
	c.f.mu.Lock()
	first := !c.closed
	c.closed = true
	if first {
		c.f.open--
	}
	c.f.mu.Unlock()
	if !first {
		return nil
	}
	err := c.base.Close()
	if err != nil && !c.base.(driver.Validator).IsValid() {
		return nil // already closed by kill
	}
	return err
}

type dualFaultStmt struct {
	f    *dualFault
	base driver.Stmt
}

func (s *dualFaultStmt) Close() error  { return s.base.Close() }
func (s *dualFaultStmt) NumInput() int { return s.base.NumInput() }

func (s *dualFaultStmt) Exec([]driver.Value) (driver.Result, error) {
	return nil, errors.New("dual fault: legacy Stmt.Exec is not forwarded")
}

func (s *dualFaultStmt) Query([]driver.Value) (driver.Rows, error) {
	return nil, errors.New("dual fault: legacy Stmt.Query is not forwarded")
}

func (s *dualFaultStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	return s.base.(driver.StmtExecContext).ExecContext(ctx, args)
}

func (s *dualFaultStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	r, err := s.base.(driver.StmtQueryContext).QueryContext(ctx, args)
	if err != nil {
		return nil, err
	}
	return s.f.track(r), nil
}

func (f *dualFault) track(r driver.Rows) driver.Rows {
	f.mu.Lock()
	f.rows++
	f.mu.Unlock()
	return &dualFaultRows{f: f, base: r}
}

type dualFaultRows struct {
	f      *dualFault
	base   driver.Rows
	closed bool
}

func (r *dualFaultRows) Columns() []string              { return r.base.Columns() }
func (r *dualFaultRows) Next(dest []driver.Value) error { return r.base.Next(dest) }

func (r *dualFaultRows) Close() error {
	if !r.closed {
		r.closed = true
		r.f.mu.Lock()
		r.f.rows--
		r.f.mu.Unlock()
	}
	return r.base.Close()
}

func (r *dualFaultRows) ColumnTypeDatabaseTypeName(i int) string {
	return r.base.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(i)
}

func (r *dualFaultRows) HasNextResultSet() bool {
	n, ok := r.base.(driver.RowsNextResultSet)
	return ok && n.HasNextResultSet()
}

func (r *dualFaultRows) NextResultSet() error {
	if n, ok := r.base.(driver.RowsNextResultSet); ok {
		return n.NextResultSet()
	}
	return io.EOF
}

// dualFaultResult is a Result override.
type dualFaultResult struct {
	affectedErr error
	insertID    int64
}

func (r dualFaultResult) RowsAffected() (int64, error) { return 0, r.affectedErr }
func (r dualFaultResult) LastInsertId() (int64, error) { return r.insertID, nil }

// sqlStateErr is an injected reference error with a SQLSTATE.
type sqlStateErr struct{ Code, Message string }

func (e sqlStateErr) Error() string    { return e.Message }
func (e sqlStateErr) SQLState() string { return e.Code }
