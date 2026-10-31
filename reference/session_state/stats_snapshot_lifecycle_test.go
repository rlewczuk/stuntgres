package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

const statsTimestampSQL = "SELECT pg_catalog.pg_stat_get_snapshot_timestamp()"
const statsClearSQL = "SELECT pg_catalog.pg_stat_clear_snapshot()"

func statsExec(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, query string, count int64) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(query, count))
}
func statsCountSQL(table string) string {
	return "SELECT n_tup_ins FROM pg_catalog.pg_stat_user_tables WHERE relid='public." + table + "'::regclass"
}
func statsCount(t *testing.T, p *fixture.Pair, c *sql.Conn, table string) int64 {
	t.Helper()
	var n int64
	if err := c.QueryRowContext(p.Ctx, statsCountSQL(table)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func statsWantCount(t *testing.T, p *fixture.Pair, c *sql.Conn, table string, want int64) {
	t.Helper()
	if n := statsCount(t, p, c, table); n != want {
		t.Fatalf("%s statistics %d want %d", table, n, want)
	}
}
func statsTimestamp(t *testing.T, p *fixture.Pair, c *sql.Conn, stmt *sql.Stmt) sql.NullTime {
	t.Helper()
	var value sql.NullTime
	var err error
	if stmt == nil {
		err = c.QueryRowContext(p.Ctx, statsTimestampSQL).Scan(&value)
	} else {
		err = stmt.QueryRowContext(p.Ctx).Scan(&value)
	}
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func statsNoTimestamp(t *testing.T, p *fixture.Pair, c *sql.Conn, stmt *sql.Stmt) {
	t.Helper()
	if value := statsTimestamp(t, p, c, stmt); value.Valid {
		t.Fatal("unexpected eager/retained snapshot", value)
	}
}
func statsClear(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(statsClearSQL, []string{"pg_stat_clear_snapshot"}, []string{"VOID"}, [][]any{{""}}))
}

func statsRetire(t *testing.T, writer *sql.Conn) {
	t.Helper()
	if err := writer.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
		t.Fatalf("physical retirement: %v", err)
	}
}

// Physical retirement is a public terminal boundary, never a SQL flush helper.
// The independent none-mode probe waits for actual server exit publication.
func statsPublish(t *testing.T, p *fixture.Pair, b fixture.Backend, table string, want int64, commands ...fixture.Step) {
	t.Helper()
	writer, retire := statsReportingConn(t, p, b)
	for _, step := range commands {
		fixture.RunStep(t, p.Ctx, writer, b.Recorder, step)
	}
	retire()
	statsAwait(t, p, b, table, "n_tup_ins", want)
}
func statsAwait(t *testing.T, p *fixture.Pair, b fixture.Backend, table, column string, want int64) {
	t.Helper()
	probe, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	fixture.RunStep(t, p.Ctx, probe, b.Recorder, fixture.Exec("SET stats_fetch_consistency=none", 0))
	ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
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
func statsCreate(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, names ...string) {
	t.Helper()
	for _, name := range names {
		statsExec(t, p, b, c, "CREATE TABLE "+name+"(id integer)", 0)
		statsExec(t, p, b, c, "ALTER TABLE "+name+" SET (autovacuum_enabled=false)", 0)
	}
}
func TestReferenceStatsSnapshotLifecycle(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		statsCreate(t, p, b, c, "stats_life")
		stamp, err := c.PrepareContext(p.Ctx, statsTimestampSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer stamp.Close()
		statsNoTimestamp(t, p, c, stamp)
		statsExec(t, p, b, c, "SET stats_fetch_consistency=snapshot", 0)
		for _, terminal := range []string{"COMMIT", "ROLLBACK"} {
			statsExec(t, p, b, c, "BEGIN", 0)
			defer c.ExecContext(context.Background(), "ROLLBACK")
			lower := time.Now().Add(-time.Second)
			statsNoTimestamp(t, p, c, stamp)
			statsWantCount(t, p, c, "stats_life", 0)
			first := statsTimestamp(t, p, c, stamp)
			if !first.Valid || first.Time.Before(lower) || first.Time.After(time.Now().Add(time.Second)) {
				t.Fatal("missing/out-of-bounds first capture", first)
			}
			if second := statsTimestamp(t, p, c, nil); !second.Valid || !second.Time.Equal(first.Time) {
				t.Fatal("unstable snapshot timestamp", first, second)
			}
			statsExec(t, p, b, c, terminal, 0)
			statsNoTimestamp(t, p, c, stamp)
		}
		statsWantCount(t, p, c, "stats_life", 0)
		statsNoTimestamp(t, p, c, stamp)
		statsExec(t, p, b, c, "BEGIN", 0)
		for _, terminal := range []string{"COMMIT AND CHAIN", "ROLLBACK AND CHAIN"} {
			statsWantCount(t, p, c, "stats_life", 0)
			if !statsTimestamp(t, p, c, stamp).Valid {
				t.Fatal("missing old chained capture")
			}
			statsExec(t, p, b, c, terminal, 0)
			statsNoTimestamp(t, p, c, stamp)
		}
		statsExec(t, p, b, c, "ROLLBACK", 0)
		statsExec(t, p, b, c, "BEGIN", 0)
		statsWantCount(t, p, c, "stats_life", 0)
		before := statsTimestamp(t, p, c, stamp)
		statsPublish(t, p, b, "stats_life", 2, fixture.Exec("INSERT INTO stats_life VALUES (1),(2)", 2))
		statsWantCount(t, p, c, "stats_life", 0)
		if after := statsTimestamp(t, p, c, stamp); !after.Time.Equal(before.Time) {
			t.Fatal("peer changed timestamp")
		}
		clear, err := c.PrepareContext(p.Ctx, statsClearSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer clear.Close()
		if after := statsTimestamp(t, p, c, stamp); !after.Valid || !after.Time.Equal(before.Time) {
			t.Fatal("Prepare cleared the existing snapshot")
		}
		heldClear := fixture.HeldStatement{Executor: c, Statement: clear, SQL: statsClearSQL}
		fixture.RunStep(t, p.Ctx, heldClear, b.Recorder, fixture.Select(statsClearSQL, []string{"pg_stat_clear_snapshot"}, []string{"VOID"}, [][]any{{""}}))
		statsNoTimestamp(t, p, c, stamp)
		statsWantCount(t, p, c, "stats_life", 2)
		rebuilt := statsTimestamp(t, p, c, stamp)
		if !rebuilt.Valid || rebuilt.Time.Before(before.Time) {
			t.Fatal("invalid rebuilt timestamp", rebuilt, before)
		}
		fixture.RunStep(t, p.Ctx, heldClear, b.Recorder, fixture.Select(statsClearSQL, []string{"pg_stat_clear_snapshot"}, []string{"VOID"}, [][]any{{""}}))
		statsNoTimestamp(t, p, c, stamp)
		statsWantCount(t, p, c, "stats_life", 2)
		statsClear(t, p, b, c)
		statsNoTimestamp(t, p, c, stamp)
		statsExec(t, p, b, c, "COMMIT", 0)
		statsNoTimestamp(t, p, c, stamp)
	})
}
func TestReferenceStatsSnapshotModes(t *testing.T) {
	for _, mode := range []string{"none", "cache", "snapshot"} {
		for _, byName := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/name_%t", mode, byName), func(t *testing.T) {
				activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					statsCreate(t, p, b, c, "stats_x", "stats_y")
					statsPublish(t, p, b, "stats_x", 1, fixture.Exec("INSERT INTO stats_x VALUES (1)", 1))
					statsExec(t, p, b, c, "SET stats_fetch_consistency="+mode, 0)
					statsExec(t, p, b, c, "BEGIN", 0)
					defer c.ExecContext(context.Background(), "ROLLBACK")
					query := statsCountSQL("stats_x")
					if byName {
						query = "SELECT n_tup_ins FROM pg_catalog.pg_stat_user_tables WHERE relname='stats_x'"
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(query, []string{"n_tup_ins"}, []string{"INT8"}, [][]any{{int64(1)}}))
					statsPublish(t, p, b, "stats_y", 2, fixture.Exec("INSERT INTO stats_y VALUES (1),(2)", 2))
					statsPublish(t, p, b, "stats_x", 3, fixture.Exec("INSERT INTO stats_x VALUES (2),(3)", 2))
					wantX, wantY := int64(1), int64(2)
					if mode == "none" {
						wantX = 3
					}
					if mode == "snapshot" {
						wantY = 0
					}
					statsWantCount(t, p, c, "stats_x", wantX)
					statsWantCount(t, p, c, "stats_y", wantY)
					if mode != "snapshot" {
						statsNoTimestamp(t, p, c, nil)
					}
					statsClear(t, p, b, c)
					statsWantCount(t, p, c, "stats_x", 3)
					statsWantCount(t, p, c, "stats_y", 2)
					for _, view := range []string{"pg_stat_all_tables", "pg_stat_user_tables"} {
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT n_tup_ins FROM pg_catalog."+view+" WHERE relid='public.stats_x'::regclass", []string{"n_tup_ins"}, []string{"INT8"}, [][]any{{int64(3)}}))
					}
					statsExec(t, p, b, c, "ROLLBACK", 0)
				})
			})
		}
	}
}
func TestReferenceStatsSnapshotInvalidation(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		statsCreate(t, p, b, c, "stats_invalidate")
		statsExec(t, p, b, c, "SET stats_fetch_consistency=snapshot", 0)
		statsExec(t, p, b, c, "BEGIN", 0)
		defer c.ExecContext(context.Background(), "ROLLBACK")
		capture := func() {
			statsWantCount(t, p, c, "stats_invalidate", 0)
			if !statsTimestamp(t, p, c, nil).Valid {
				t.Fatal("missing capture")
			}
		}
		capture()
		statsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
		statsExec(t, p, b, c, "SET stats_fetch_consistency=snapshot", 0)
		statsNoTimestamp(t, p, c, nil)
		capture()
		statsExec(t, p, b, c, "SET stats_fetch_consistency=snapshot", 0)
		if !statsTimestamp(t, p, c, nil).Valid {
			t.Fatal("unchanged assignment invalidated")
		}
		statsExec(t, p, b, c, "SAVEPOINT stats_mark", 0)
		statsExec(t, p, b, c, "SET LOCAL stats_fetch_consistency=none", 0)
		statsExec(t, p, b, c, "ROLLBACK TO stats_mark", 0)
		statsNoTimestamp(t, p, c, nil)
		capture()
		statsExec(t, p, b, c, "RESET ALL", 0)
		statsNoTimestamp(t, p, c, nil)
		statsExec(t, p, b, c, "ROLLBACK", 0)
		statsExec(t, p, b, c, "BEGIN", 0)
		statsExec(t, p, b, c, "SET LOCAL stats_fetch_consistency=snapshot", 0)
		capture()
		statsExec(t, p, b, c, "COMMIT", 0)
		statsNoTimestamp(t, p, c, nil)
		statsExec(t, p, b, c, "CREATE FUNCTION stats_saved() RETURNS void LANGUAGE plpgsql SET stats_fetch_consistency='none' AS $$BEGIN PERFORM 1; END$$", 0)
		statsExec(t, p, b, c, "SET stats_fetch_consistency=snapshot", 0)
		statsExec(t, p, b, c, "BEGIN", 0)
		capture()
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT stats_saved()", []string{"stats_saved"}, []string{"VOID"}, [][]any{{""}}))
		statsNoTimestamp(t, p, c, nil)
		statsExec(t, p, b, c, "ROLLBACK", 0)
	})
}
