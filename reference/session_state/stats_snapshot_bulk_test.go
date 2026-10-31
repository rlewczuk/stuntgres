package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func statsBulkTracking(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	statsCreate(t, p, b, c, "stats_bulk_source")
	statsExec(t, p, b, c, "INSERT INTO stats_bulk_source VALUES (1),(2)", 2)
	// Collection policy belongs to actual population, not its later COMMIT.
	statsPublish(t, p, b, "stats_bulk_table", 2,
		fixture.Exec("BEGIN", 0), fixture.Exec("CREATE TABLE stats_bulk_table AS SELECT id FROM stats_bulk_source", 2), fixture.Exec("SET track_counts=off", 0), fixture.Exec("COMMIT", 0))
	statsPublish(t, p, b, "stats_bulk_off_mv", 0,
		fixture.Exec("BEGIN", 0), fixture.Exec("SET track_counts=off", 0), fixture.Exec("CREATE MATERIALIZED VIEW stats_bulk_off_mv AS SELECT id FROM stats_bulk_source", 2), fixture.Exec("SET track_counts=on", 0), fixture.Exec("COMMIT", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id FROM stats_bulk_off_mv ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
	statsPublish(t, p, b, "stats_bulk_mv", 2, fixture.Exec("CREATE MATERIALIZED VIEW stats_bulk_mv AS SELECT id FROM stats_bulk_source", 2))
	statsExec(t, p, b, c, "CREATE UNIQUE INDEX stats_bulk_mv_key ON stats_bulk_mv(id)", 0)
	statsPublish(t, p, b, "stats_bulk_mv", 2, fixture.Exec("REFRESH MATERIALIZED VIEW CONCURRENTLY stats_bulk_mv", 0))
	statsExec(t, p, b, c, "INSERT INTO stats_bulk_source VALUES (3)", 1)
	statsPublish(t, p, b, "stats_bulk_mv", 3, fixture.Exec("REFRESH MATERIALIZED VIEW CONCURRENTLY stats_bulk_mv", 0))
	statsExec(t, p, b, c, "DELETE FROM stats_bulk_source WHERE id=1", 1)
	statsPublish(t, p, b, "stats_bulk_mv", 3, fixture.Exec("REFRESH MATERIALIZED VIEW CONCURRENTLY stats_bulk_mv", 0))
	statsAwait(t, p, b, "stats_bulk_mv", "n_tup_del", 1)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT n_tup_ins,n_tup_upd,n_tup_del FROM pg_stat_user_tables WHERE relid='stats_bulk_mv'::regclass", []string{"n_tup_ins", "n_tup_upd", "n_tup_del"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(3), int64(0), int64(1)}}))
	statsPublish(t, p, b, "stats_bulk_mv", 5, fixture.Exec("REFRESH MATERIALIZED VIEW stats_bulk_mv", 0))
	statsAwait(t, p, b, "stats_bulk_mv", "n_live_tup", 2)
	statsPublish(t, p, b, "stats_bulk_mv", 5, fixture.Exec("REFRESH MATERIALIZED VIEW stats_bulk_mv WITH NO DATA", 0))
	statsAwait(t, p, b, "stats_bulk_mv", "n_live_tup", 0)
	statsPublish(t, p, b, "stats_bulk_mv", 5, fixture.Exec("SET track_counts=off", 0), fixture.Exec("REFRESH MATERIALIZED VIEW stats_bulk_mv", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id FROM stats_bulk_mv ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}}))
	statsWantCount(t, p, c, "stats_bulk_mv", 5)
	statsPublish(t, p, b, "stats_bulk_mv", 7, fixture.Exec("REFRESH MATERIALIZED VIEW stats_bulk_mv", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT n_tup_ins,n_tup_upd,n_tup_del,n_live_tup,n_dead_tup FROM pg_stat_user_tables WHERE relid='stats_bulk_mv'::regclass", []string{"n_tup_ins", "n_tup_upd", "n_tup_del", "n_live_tup", "n_dead_tup"}, []string{"INT8", "INT8", "INT8", "INT8", "INT8"}, [][]any{{int64(7), int64(0), int64(1), int64(2), int64(0)}}))
}
