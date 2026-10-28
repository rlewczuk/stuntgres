package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

var copyProgressNames = []string{"pid", "datid", "datname", "relid", "command", "type", "bytes_processed", "bytes_total", "tuples_processed", "tuples_excluded", "tuples_skipped"}
var copyProgressTypes = []string{"INT4", "OID", "NAME", "OID", "TEXT", "TEXT", "INT8", "INT8", "INT8", "INT8", "INT8"}

func copyProgressScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func copyProgressConn(t *testing.T, p *fixture.Pair, db *sql.DB) *sql.Conn {
	t.Helper()
	c, err := db.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil && err != sql.ErrConnDone {
			t.Error(err)
		}
	})
	return c
}
func copyProgressExec(t *testing.T, ctx context.Context, c fixture.Executor, q string) {
	t.Helper()
	if _, err := c.ExecContext(ctx, q); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

type copyProgressIdentity struct {
	pid, datid, relid int64
	database          string
}

func copyProgressIdentify(t *testing.T, p *fixture.Pair, c *sql.Conn, table string) copyProgressIdentity {
	t.Helper()
	var id copyProgressIdentity
	if err := c.QueryRowContext(p.Ctx, "SELECT pg_backend_pid(),d.oid::bigint,current_database() FROM pg_database d WHERE datname=current_database()").Scan(&id.pid, &id.datid, &id.database); err != nil {
		t.Fatal(err)
	}
	if table != "" {
		if err := c.QueryRowContext(p.Ctx, "SELECT '"+table+"'::regclass::oid::bigint").Scan(&id.relid); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

type copyProgressSnapshot struct {
	pid, datid                                 int64
	database                                   string
	relid                                      sql.NullInt64
	command, endpoint                          sql.NullString
	bytes, total, processed, excluded, skipped sql.NullInt64
}

func copyProgressRead(t *testing.T, p *fixture.Pair, c *sql.Conn, pid int64) (copyProgressSnapshot, bool) {
	t.Helper()
	var s copyProgressSnapshot
	err := c.QueryRowContext(p.Ctx, "SELECT * FROM pg_catalog.pg_stat_progress_copy WHERE pid=$1", pid).Scan(&s.pid, &s.datid, &s.database, &s.relid, &s.command, &s.endpoint, &s.bytes, &s.total, &s.processed, &s.excluded, &s.skipped)
	if err == sql.ErrNoRows {
		return s, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return s, true
}
func copyProgressAwait(t *testing.T, p *fixture.Pair, c *sql.Conn, pid int64, predicate func(copyProgressSnapshot, bool) bool) copyProgressSnapshot {
	t.Helper()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		s, ok := copyProgressRead(t, p, c, pid)
		if predicate(s, ok) {
			return s
		}
		select {
		case <-tick.C:
		case <-p.Ctx.Done():
			t.Fatalf("progress condition pid=%d last=%+v present=%v: %v", pid, s, ok, p.Ctx.Err())
		}
	}
}
func copyProgressVisible(t *testing.T, s copyProgressSnapshot, id copyProgressIdentity, command string) {
	t.Helper()
	if s.pid != id.pid || s.datid != id.datid || s.database != id.database || !s.relid.Valid || s.relid.Int64 != id.relid || s.command.String != command || !s.command.Valid || !s.endpoint.Valid || s.endpoint.String != "PIPE" {
		t.Fatalf("identity=%+v expected=%+v %s", s, id, command)
	}
	for _, v := range []sql.NullInt64{s.bytes, s.total, s.processed, s.excluded, s.skipped} {
		if !v.Valid || v.Int64 < 0 {
			t.Fatalf("invalid counter %+v in %+v", v, s)
		}
	}
	if s.total.Int64 != 0 {
		t.Fatalf("PIPE total must be zero: %+v", s)
	}
}
func copyProgressMasked(t *testing.T, s copyProgressSnapshot, id copyProgressIdentity) {
	t.Helper()
	if s.pid != id.pid || s.datid != id.datid || s.database != id.database || s.relid.Valid || s.command.Valid || s.endpoint.Valid || s.bytes.Valid || s.total.Valid || s.processed.Valid || s.excluded.Valid || s.skipped.Valid {
		t.Fatalf("expected public identity and eight NULLs: %+v", s)
	}
}
func copyProgressIdle(t *testing.T, p *fixture.Pair, c *sql.Conn, pid int64) {
	t.Helper()
	if s, ok := copyProgressRead(t, p, c, pid); ok {
		t.Fatalf("retained progress %+v", s)
	}
}

type copyProgressChunk struct {
	data []byte
	err  error
}
type copyProgressInput struct {
	chunks  chan copyProgressChunk
	pending []byte
	stop    chan struct{}
	once    sync.Once
}

func newCopyProgressInput() *copyProgressInput {
	return &copyProgressInput{chunks: make(chan copyProgressChunk), stop: make(chan struct{})}
}
func (r *copyProgressInput) Read(dst []byte) (int, error) {
	if len(r.pending) > 0 {
		n := copy(dst, r.pending)
		r.pending = r.pending[n:]
		return n, nil
	}
	select {
	case <-r.stop:
		return 0, io.EOF
	case chunk := <-r.chunks:
		if len(chunk.data) == 0 {
			return 0, chunk.err
		}
		r.pending = chunk.data
		return r.Read(dst)
	}
}
func (r *copyProgressInput) close() { r.once.Do(func() { close(r.stop) }) }
func (r *copyProgressInput) send(t *testing.T, p *fixture.Pair, data []byte) {
	t.Helper()
	select {
	case r.chunks <- copyProgressChunk{data: data}:
	case <-p.Ctx.Done():
		t.Fatal(p.Ctx.Err())
	}
}

type copyProgressOutcome struct {
	result stuntgres.CopyResult
	err    error
}
type copyProgressRun struct {
	input    *copyProgressInput
	done     chan copyProgressOutcome
	finished chan struct{}
	cancel   context.CancelFunc
	joined   bool
}

func copyProgressStart(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string) *copyProgressRun {
	t.Helper()
	ctx, cancel := context.WithCancel(p.Ctx)
	r := &copyProgressRun{input: newCopyProgressInput(), done: make(chan copyProgressOutcome, 1), finished: make(chan struct{}), cancel: cancel}
	go func() {
		defer close(r.finished)
		result, err := fixture.CopyFrom(ctx, c, b.PG, q, r.input)
		r.done <- copyProgressOutcome{result, err}
	}()
	t.Cleanup(func() {
		cancel()
		r.input.close()
		if !r.joined {
			<-r.done
		}
	})
	return r
}
func (r *copyProgressRun) finish(t *testing.T, count int64) {
	t.Helper()
	r.input.close()
	out := <-r.done
	r.joined = true
	if out.err != nil || out.result.Count != count || out.result.CommandTag != fmt.Sprintf("COPY %d", count) {
		t.Fatalf("COPY outcome=%+v expected count=%d", out, count)
	}
}
func copyProgressIntegers(start, count int) []byte {
	var s strings.Builder
	for i := start; i < start+count; i++ {
		fmt.Fprintf(&s, "%d\n", i)
	}
	return []byte(s.String())
}

func replaceCopyProgressName(q string) string {
	return strings.ReplaceAll(q, "pg_stat_progress_copy", "pg_catalog.pg_stat_progress_copy")
}
