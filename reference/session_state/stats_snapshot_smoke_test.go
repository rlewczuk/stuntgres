package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStatsSnapshotSmoke(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		statsCreate(t, p, b, c, "stats_smoke")
		timestamp, err := c.PrepareContext(p.Ctx, statsTimestampSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer timestamp.Close()
		query := statsCountSQL("stats_smoke")
		reader, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		held := fixture.HeldStatement{Executor: c, Statement: reader, SQL: query}
		statsExec(t, p, b, c, "SET stats_fetch_consistency=snapshot", 0)
		statsExec(t, p, b, c, "BEGIN", 0)
		defer c.ExecContext(context.Background(), "ROLLBACK")
		statsNoTimestamp(t, p, c, timestamp)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, []string{"n_tup_ins"}, []string{"INT8"}, [][]any{{int64(0)}}))
		first := statsTimestamp(t, p, c, timestamp)
		if !first.Valid {
			t.Fatal("no lazy capture")
		}
		statsPublish(t, p, b, "stats_smoke", 2, fixture.Exec("INSERT INTO stats_smoke VALUES (1),(2)", 2))
		statsWantCount(t, p, c, "stats_smoke", 0)
		if v := statsTimestamp(t, p, c, timestamp); !v.Valid || !v.Time.Equal(first.Time) {
			t.Fatal("snapshot changed after publication")
		}
		statsClear(t, p, b, c)
		statsNoTimestamp(t, p, c, timestamp)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, []string{"n_tup_ins"}, []string{"INT8"}, [][]any{{int64(2)}}))
		if v := statsTimestamp(t, p, c, timestamp); !v.Valid || v.Time.Before(first.Time) {
			t.Fatal("rebuild did not construct timestamp")
		}
		statsExec(t, p, b, c, "ROLLBACK", 0)
		statsNoTimestamp(t, p, c, timestamp)
		statsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
		statsExec(t, p, b, c, "BEGIN", 0)
		statsWantCount(t, p, c, "stats_smoke", 2)
		statsPublish(t, p, b, "stats_smoke", 3, fixture.Exec("INSERT INTO stats_smoke VALUES (3)", 1))
		statsWantCount(t, p, c, "stats_smoke", 3)
		statsNoTimestamp(t, p, c, timestamp)
		statsPublish(t, p, b, "stats_smoke", 3, fixture.Exec("SET track_counts=off", 0), fixture.Exec("INSERT INTO stats_smoke VALUES (4)", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM stats_smoke", []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}}))
		statsWantCount(t, p, c, "stats_smoke", 3)
		statsPublish(t, p, b, "stats_smoke", 4, fixture.Exec("SET track_counts=off", 0), fixture.Exec("SET track_counts=on", 0), fixture.Exec("INSERT INTO stats_smoke VALUES (5)", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM stats_smoke", []string{"count"}, []string{"INT8"}, [][]any{{int64(5)}}))
		statsWantCount(t, p, c, "stats_smoke", 4)
		statsExec(t, p, b, c, "COMMIT", 0)
		statsCreate(t, p, b, c, "stats_smoke_abort", "stats_smoke_truncate")
		statsPublish(t, p, b, "stats_smoke_abort", 2, fixture.Exec("BEGIN", 0), fixture.Exec("INSERT INTO stats_smoke_abort VALUES(1),(2)", 2), fixture.Exec("ROLLBACK", 0))
		statsReportingWant(t, p, b, c, "stats_smoke_abort", statsReportingVector{2, 0, 0, 0, 2, 0, 2})
		statsPublish(t, p, b, "stats_smoke_truncate", 1, fixture.Exec("BEGIN", 0), fixture.Exec("INSERT INTO stats_smoke_truncate VALUES(1),(2)", 2), fixture.Exec("TRUNCATE stats_smoke_truncate", 0), fixture.Exec("INSERT INTO stats_smoke_truncate VALUES(3)", 1), fixture.Exec("COMMIT", 0))
		statsReportingWant(t, p, b, c, "stats_smoke_truncate", statsReportingVector{1, 0, 0, 1, 0, 1, 1})
		statsReportingOpenSmoke(t, p, b, c)
		statsMaintenanceOpenSmoke(t, p, b, c)
	})
}
func TestReferenceStatsSnapshotTracking(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		statsCreate(t, p, b, c, "stats_tracking")
		statsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
		statsPublish(t, p, b, "stats_tracking", 1, fixture.Exec("INSERT INTO stats_tracking VALUES (1)", 1))
		statsPublish(t, p, b, "stats_tracking", 1, fixture.Exec("SET track_counts=off", 0), fixture.Exec("INSERT INTO stats_tracking VALUES (2)", 1), fixture.Exec("UPDATE stats_tracking SET id=id+10", 2), fixture.Exec("DELETE FROM stats_tracking WHERE id=11", 1), fixture.Exec("ANALYZE stats_tracking", 0), fixture.Exec("VACUUM stats_tracking", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id FROM stats_tracking", []string{"id"}, []string{"INT4"}, [][]any{{int64(12)}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT n_tup_ins,n_tup_upd,n_tup_del,vacuum_count,analyze_count,last_vacuum,last_analyze FROM pg_stat_user_tables WHERE relid='stats_tracking'::regclass", []string{"n_tup_ins", "n_tup_upd", "n_tup_del", "vacuum_count", "analyze_count", "last_vacuum", "last_analyze"}, []string{"INT8", "INT8", "INT8", "INT8", "INT8", "TIMESTAMPTZ", "TIMESTAMPTZ"}, [][]any{{int64(1), int64(0), int64(0), int64(0), int64(0), nil, nil}}))
		statsPublish(t, p, b, "stats_tracking", 2, fixture.Exec("INSERT INTO stats_tracking VALUES (3)", 1))
		statsWantCount(t, p, c, "stats_tracking", 2)
		// Activity tracking never changes current_query's independent command authority.
		statsExec(t, p, b, c, "SET track_activities=off", 0)
		const q = "SELECT current_query()"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(q, []string{"current_query"}, []string{"TEXT"}, [][]any{{q}}))
		statsBulkTracking(t, p, b, c)
	})
}
