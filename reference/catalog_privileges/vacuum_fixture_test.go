package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"runtime"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func vacuumScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func vacuumConn(t *testing.T, p *fixture.Pair, b fixture.Backend) *sql.Conn {
	t.Helper()
	c, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	return c
}

func vacuumRun(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
}

func vacuumHeld(t *testing.T, p *fixture.Pair, c *sql.Conn, q string) fixture.HeldStatement {
	t.Helper()
	s, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal("quiet Prepare", q, err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return fixture.HeldStatement{Executor: c, Statement: s, SQL: q}
}

func vacuumWarning(t *testing.T, got []fixture.Diagnostic, code, message string) {
	t.Helper()
	want := []fixture.Diagnostic{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: code, Message: message}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("warnings got %#v want %#v", got, want)
	}
}

func vacuumRows(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, table string, ids ...int64) {
	t.Helper()
	var rows [][]any
	for _, id := range ids {
		rows = append(rows, []any{id})
	}
	vacuumRun(t, p, b, c, fixture.Select("SELECT id FROM "+table+" ORDER BY id", []string{"id"}, []string{"INT4"}, rows))
}

// PostgreSQL cumulative statistics may become visible only after backend flush.
// Each fresh autocommit read abandons the previous statistics snapshot.
func vacuumCount(t *testing.T, ctx context.Context, c *sql.Conn, table string, want int64) {
	t.Helper()
	vacuumStatistic(t, ctx, c, table, "vacuum_count", want)
}

func vacuumStatistic(t *testing.T, ctx context.Context, c *sql.Conn, table, column string, want int64) int64 {
	t.Helper()
	deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		var got int64
		err := c.QueryRowContext(deadline, "SELECT "+column+" FROM pg_stat_all_tables WHERE relid=$1::regclass", table).Scan(&got)
		if err != nil {
			t.Fatal(err)
		}
		if got == want {
			return got
		}
		// Resettable counters may temporarily retain a pre-flush larger value.
		// Require exact equality within the original bounded fresh-read window.
		if deadline.Err() != nil {
			t.Fatalf("%s %s=%d want %d", table, column, got, want)
		}
		runtime.Gosched()
	}
}
