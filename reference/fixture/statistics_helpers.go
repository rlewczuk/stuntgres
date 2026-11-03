package fixture

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
	"time"
)

// StatisticsScenario runs one public SQL scenario against each configured backend.
func StatisticsScenario(t *testing.T, body func(*testing.T, *Pair, Backend, *sql.Conn)) {
	t.Helper()
	p := NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

// StatisticsExec records SQL with an independently specified affected-row count.
func StatisticsExec(t *testing.T, p *Pair, b Backend, c *sql.Conn, query string, count int64) {
	t.Helper()
	RunStep(t, p.Ctx, c, b.Recorder, Exec(query, count))
}

// StatisticsClearSnapshot clears the reader cut, without publishing or resetting facts.
func StatisticsClearSnapshot(t *testing.T, p *Pair, b Backend, c *sql.Conn) {
	t.Helper()
	RunStep(t, p.Ctx, c, b.Recorder, Select("SELECT pg_catalog.pg_stat_clear_snapshot()", []string{"pg_stat_clear_snapshot"}, []string{"VOID"}, [][]any{{""}}))
}

// StatisticsRetire uses the public physical-session terminal boundary, not a SQL flush.
func StatisticsRetire(t *testing.T, writer *sql.Conn) {
	t.Helper()
	if err := writer.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
		t.Fatalf("physical retirement: %v", err)
	}
}

// StatisticsReportingConn retains a writer until explicit retirement or bounded cleanup.
func StatisticsReportingConn(t *testing.T, p *Pair, b Backend) (*sql.Conn, func()) {
	t.Helper()
	c, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	owned := true
	t.Cleanup(func() {
		if owned {
			ctx, cancel := context.WithTimeout(context.Background(), Liveness)
			defer cancel()
			if _, err := c.ExecContext(ctx, "ROLLBACK"); err != nil {
				t.Errorf("reporting rollback cleanup: %v", err)
			}
			if err := c.Close(); err != nil {
				t.Error(err)
			}
		}
	})
	return c, func() {
		StatisticsRetire(t, c)
		owned = false
	}
}

// StatisticsAwaitTableCount observes actual publication using an independent none-mode reader.
func StatisticsAwaitTableCount(t *testing.T, p *Pair, b Backend, table, column string, want int64) {
	t.Helper()
	probe, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	RunStep(t, p.Ctx, probe, b.Recorder, Exec("SET stats_fetch_consistency=none", 0))
	ctx, cancel := context.WithTimeout(p.Ctx, Liveness)
	defer cancel()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	var last int64
	query := "SELECT " + column + " FROM pg_catalog.pg_stat_user_tables WHERE relid='public." + table + "'::regclass"
	for {
		if err = probe.QueryRowContext(ctx, query).Scan(&last); err != nil {
			t.Fatalf("publication probe %s last=%d: %v", column, last, err)
		}
		if last == want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("publication expired last=%d want=%d: %v", last, want, ctx.Err())
		case <-tick.C:
		}
	}
}

// StatisticsCreateTables creates tracked marker tables without background maintenance.
func StatisticsCreateTables(t *testing.T, p *Pair, b Backend, c *sql.Conn, names ...string) {
	t.Helper()
	for _, name := range names {
		StatisticsExec(t, p, b, c, "CREATE TABLE "+name+"(id integer)", 0)
		StatisticsExec(t, p, b, c, "ALTER TABLE "+name+" SET (autovacuum_enabled=false)", 0)
	}
}
