package dual

import (
	"bytes"
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Test-local driver fakes. No SQL is issued: queries are opaque strings.

type fakeConnector struct{ conn driver.Conn }

func (f *fakeConnector) Connect(context.Context) (driver.Conn, error) { return f.conn, nil }
func (f *fakeConnector) Driver() driver.Driver                        { return fakeDriver{} }

type fakeDriver struct{}

func (fakeDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("fake: Open is not supported")
}

// fakeConn is a minimal driver.Conn; stmt chooses the prepared statement.
type fakeConn struct {
	stmt   func() driver.Stmt
	closes atomic.Int32
}

func (c *fakeConn) Prepare(string) (driver.Stmt, error) {
	if c.stmt == nil {
		return &fakeStmt{}, nil
	}
	return c.stmt(), nil
}
func (c *fakeConn) Close() error              { c.closes.Add(1); return nil }
func (c *fakeConn) Begin() (driver.Tx, error) { return fakeTx{}, nil }

// execConn adds ExecerContext and QueryerContext backed by functions.
type execConn struct {
	*fakeConn
	exec  func(ctx context.Context, query string) (driver.Result, error)
	query func(ctx context.Context, query string) (driver.Rows, error)
}

func (c *execConn) ExecContext(ctx context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	return c.exec(ctx, query)
}

func (c *execConn) QueryContext(ctx context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	return c.query(ctx, query)
}

type fakeStmt struct{}

func (*fakeStmt) Close() error  { return nil }
func (*fakeStmt) NumInput() int { return -1 }
func (*fakeStmt) Exec([]driver.Value) (driver.Result, error) {
	return driver.RowsAffected(1), nil
}
func (*fakeStmt) Query([]driver.Value) (driver.Rows, error) { return oneRow(), nil }

type fakeTx struct{}

func (fakeTx) Commit() error   { return nil }
func (fakeTx) Rollback() error { return nil }

// fakeRows implements only driver.Rows: no ColumnType* and no RowsNextResultSet.
type fakeRows struct {
	columns []string
	data    [][]driver.Value
	next    int
}

func oneRow() *fakeRows {
	return &fakeRows{columns: []string{"a"}, data: [][]driver.Value{{int64(1)}}}
}

func (r *fakeRows) Columns() []string { return r.columns }
func (r *fakeRows) Close() error      { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.next >= len(r.data) {
		return io.EOF
	}
	copy(dest, r.data[r.next])
	r.next++
	return nil
}

// Interface carriers for building natives with exact interface subsets.
type (
	resetMixin    struct{}
	validMixin    struct{}
	checkMixin    struct{}
	converterMix  struct{}
	nopConverter  struct{}
	stmtCheckMix  struct{}
	stmtWithCheck struct {
		*fakeStmt
		stmtCheckMix
	}
	stmtWithConverter struct {
		*fakeStmt
		converterMix
	}
	stmtWithBoth struct {
		*fakeStmt
		stmtCheckMix
		converterMix
	}
)

func (resetMixin) ResetSession(context.Context) error          { return nil }
func (validMixin) IsValid() bool                               { return true }
func (checkMixin) CheckNamedValue(*driver.NamedValue) error    { return nil }
func (stmtCheckMix) CheckNamedValue(*driver.NamedValue) error  { return nil }
func (converterMix) ColumnConverter(int) driver.ValueConverter { return nopConverter{} }
func (nopConverter) ConvertValue(v any) (driver.Value, error)  { return v, nil }

// lockedLog is a goroutine-safe io.Writer that counts Write calls.
type lockedLog struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	writes []string
}

func (l *lockedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.writes = append(l.writes, string(p))
	return l.buf.Write(p)
}

// records decodes every logged line into a generic map.
func (l *lockedLog) records(t *testing.T) []map[string]any {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []map[string]any
	for _, line := range bytes.Split(bytes.TrimSuffix(l.buf.Bytes(), []byte("\n")), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("record %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

// waitFor polls cond with a 1 ms tick for at most 30 s.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		<-tick.C
	}
}

// diffOf returns a record's response_diff as strings.
func diffOf(m map[string]any) []string {
	raw, _ := m["response_diff"].([]any)
	var out []string
	for _, line := range raw {
		s, _ := line.(string)
		out = append(out, s)
	}
	return out
}
