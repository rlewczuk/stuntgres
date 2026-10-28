package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

var indexProgressNames = []string{"pid", "datid", "datname", "relid", "index_relid", "command", "phase", "lockers_total", "lockers_done", "current_locker_pid", "blocks_total", "blocks_done", "tuples_total", "tuples_done", "partitions_total", "partitions_done"}
var indexProgressTypes = []string{"INT4", "OID", "NAME", "OID", "OID", "TEXT", "TEXT", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8"}

type indexProgressSnapshot struct {
	pid, datid     int64
	database       string
	relid, index   sql.NullInt64
	command, phase sql.NullString
	counters       [9]sql.NullInt64
}

func indexProgressRead(t *testing.T, p *fixture.Pair, c fixture.Executor, pid int64) (indexProgressSnapshot, bool) {
	t.Helper()
	var s indexProgressSnapshot
	args := []any{&s.pid, &s.datid, &s.database, &s.relid, &s.index, &s.command, &s.phase}
	for i := range s.counters {
		args = append(args, &s.counters[i])
	}
	rows, err := c.QueryContext(p.Ctx, "SELECT * FROM pg_catalog.pg_stat_progress_create_index WHERE pid=$1", pid)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return s, false
	}
	if err := rows.Scan(args...); err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Fatal("duplicate worker progress")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return s, true
}

func indexProgressAwait(t *testing.T, p *fixture.Pair, c fixture.Executor, pid int64, phase string, done int64, currentPID ...int64) indexProgressSnapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
	defer cancel()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		s, ok := indexProgressRead(t, p, c, pid)
		// These public witnesses hold actual backend owners. A phase label can
		// precede cohort/PID publication; require a genuinely active wait.
		if ok && s.phase.String == phase && s.counters[1].Int64 == done && s.counters[0].Int64 > done && s.counters[2].Int64 > 0 && (len(currentPID) == 0 || s.counters[2].Int64 == currentPID[0]) {
			return s
		}
		select {
		case <-tick.C:
		case <-ctx.Done():
			t.Fatalf("progress pid=%d phase=%s done=%d last=%+v present=%v: %v", pid, phase, done, s, ok, ctx.Err())
		}
	}
}

func indexProgressVisible(t *testing.T, s indexProgressSnapshot, id copyProgressIdentity, command string) {
	t.Helper()
	if s.pid != id.pid || s.datid != id.datid || s.database != id.database || !s.relid.Valid || s.relid.Int64 != id.relid || !s.index.Valid || s.index.Int64 <= 0 || !s.command.Valid || s.command.String != command || !s.phase.Valid {
		t.Fatalf("progress=%+v identity=%+v command=%s", s, id, command)
	}
	for _, n := range s.counters {
		if !n.Valid || n.Int64 < 0 {
			t.Fatalf("invalid authorized counter: %+v", s)
		}
	}
	for _, pair := range [][2]int{{0, 1}, {3, 4}, {5, 6}, {7, 8}} {
		total, done := s.counters[pair[0]].Int64, s.counters[pair[1]].Int64
		if total > 0 && done > total {
			t.Fatalf("counter exceeds known total: %+v", s)
		}
	}
	if s.counters[7].Int64 != 0 || s.counters[8].Int64 != 0 {
		t.Fatalf("nonpartitioned counters: %+v", s)
	}
}

func indexProgressMasked(t *testing.T, s indexProgressSnapshot, id copyProgressIdentity) {
	t.Helper()
	if s.pid != id.pid || s.datid != id.datid || s.database != id.database || s.relid.Valid || s.index.Valid || s.command.Valid || s.phase.Valid {
		t.Fatalf("restricted identity/details: %+v", s)
	}
	for _, n := range s.counters {
		if n.Valid {
			t.Fatalf("restricted counter is not NULL: %+v", s)
		}
	}
}

func indexProgressIdle(t *testing.T, p *fixture.Pair, c fixture.Executor, pid int64) {
	t.Helper()
	if s, ok := indexProgressRead(t, p, c, pid); ok {
		t.Fatalf("retained terminal progress: %+v", s)
	}
}

func indexProgressCancel(t *testing.T, p *fixture.Pair, b fixture.Backend, observer *sql.Conn, pid int64, cancel context.CancelFunc, join func() error) {
	t.Helper()
	if b.PG {
		var ok bool
		if err := observer.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&ok); err != nil || !ok {
			t.Fatalf("cancel=%v err=%v", ok, err)
		}
	} else {
		cancel()
	}
	err := join()
	if b.PG {
		want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
		fixture.AssertDiagnostic(t, err, &want)
	} else if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	indexProgressIdle(t, p, observer, pid)
}
