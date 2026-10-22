package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceVacuumMetadata(t *testing.T) {
	vacuumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := vacuumConn(t, p, b)
		run := func(s fixture.Step) { vacuumRun(t, p, b, c, s) }
		run(fixture.Exec("CREATE TABLE vacuum_meta(id INT)", 0))
		run(fixture.Select("SELECT reltuples FROM pg_class WHERE oid='vacuum_meta'::regclass", []string{"reltuples"}, []string{"FLOAT4"}, [][]any{{float64(-1)}}))
		run(fixture.Exec("INSERT INTO vacuum_meta VALUES(1),(2),(3)", 3))
		run(fixture.Exec("DELETE FROM vacuum_meta WHERE id=2", 1))
		// PG DML counters are backend-local until flushed. Force this writer's
		// next flush, then observe the independently specified four committed
		// modifications before testing that VACUUM does not reset them.
		if b.PG {
			if _, err := c.ExecContext(p.Ctx, "SELECT pg_stat_force_next_flush()"); err != nil {
				t.Fatal(err)
			}
		}
		baselineMods := vacuumStatistic(t, p.Ctx, c, "vacuum_meta", "n_mod_since_analyze", 4)
		var before, after time.Time
		if err := c.QueryRowContext(p.Ctx, "SELECT clock_timestamp()").Scan(&before); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("VACUUM (FREEZE) vacuum_meta", 0))
		if err := c.QueryRowContext(p.Ctx, "SELECT clock_timestamp()").Scan(&after); err != nil {
			t.Fatal(err)
		}
		vacuumCount(t, p.Ctx, c, "vacuum_meta", 1)
		var completed sql.NullTime
		if err := c.QueryRowContext(p.Ctx, "SELECT last_vacuum FROM pg_stat_all_tables WHERE relid='vacuum_meta'::regclass").Scan(&completed); err != nil {
			t.Fatal(err)
		}
		if !completed.Valid || completed.Time.Before(before) || completed.Time.After(after) {
			t.Fatalf("last_vacuum=%v outside [%v,%v]", completed, before, after)
		}
		run(fixture.Select("SELECT reltuples,relhasindex FROM pg_class WHERE oid='vacuum_meta'::regclass", []string{"reltuples", "relhasindex"}, []string{"FLOAT4", "BOOL"}, [][]any{{float64(2), false}}))
		run(fixture.Select("SELECT n_live_tup,n_dead_tup,n_ins_since_vacuum,analyze_count,autoanalyze_count FROM pg_stat_all_tables WHERE relid='vacuum_meta'::regclass", []string{"n_live_tup", "n_dead_tup", "n_ins_since_vacuum", "analyze_count", "autoanalyze_count"}, []string{"INT8", "INT8", "INT8", "INT8", "INT8"}, [][]any{{int64(2), int64(0), int64(0), int64(0), int64(0)}}))
		var mods int64
		if err := c.QueryRowContext(p.Ctx, "SELECT n_mod_since_analyze FROM pg_stat_all_tables WHERE relid='vacuum_meta'::regclass").Scan(&mods); err != nil {
			t.Fatal(err)
		}
		if mods != baselineMods {
			t.Fatalf("VACUUM reset/changed analyze modification count: %d before %d", mods, baselineMods)
		}
		vacuumRows(t, p, b, c, "vacuum_meta", 1, 3)
		vacuumFreezeBound(t, p, b, c, "vacuum_meta")
		run(fixture.Exec("VACUUM vacuum_meta", 0))
		vacuumCount(t, p.Ctx, c, "vacuum_meta", 2)
		run(fixture.Exec("CREATE INDEX vacuum_meta_ix ON vacuum_meta(id)", 0))
		run(fixture.Exec("DROP INDEX vacuum_meta_ix", 0))
		run(fixture.Select("SELECT relhasindex FROM pg_class WHERE oid='vacuum_meta'::regclass", []string{"relhasindex"}, []string{"BOOL"}, [][]any{{true}}))
		run(fixture.Exec("VACUUM vacuum_meta", 0))
		vacuumCount(t, p.Ctx, c, "vacuum_meta", 3)
		run(fixture.Select("SELECT relhasindex FROM pg_class WHERE oid='vacuum_meta'::regclass", []string{"relhasindex"}, []string{"BOOL"}, [][]any{{false}}))
		run(fixture.Exec("VACUUM (PROCESS_MAIN false,PROCESS_TOAST false) vacuum_meta", 0))
		vacuumCount(t, p.Ctx, c, "vacuum_meta", 3)
		var dbBefore, dbAfter string
		if err := c.QueryRowContext(p.Ctx, "SELECT datfrozenxid::text FROM pg_database WHERE datname=current_database()").Scan(&dbBefore); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("VACUUM (FREEZE,SKIP_DATABASE_STATS) vacuum_meta", 0))
		vacuumCount(t, p.Ctx, c, "vacuum_meta", 4)
		if err := c.QueryRowContext(p.Ctx, "SELECT datfrozenxid::text FROM pg_database WHERE datname=current_database()").Scan(&dbAfter); err != nil {
			t.Fatal(err)
		}
		if dbAfter != dbBefore {
			t.Fatalf("SKIP_DATABASE_STATS changed horizon %s -> %s", dbBefore, dbAfter)
		}
		run(fixture.Exec("VACUUM (ONLY_DATABASE_STATS)", 0))
		vacuumCount(t, p.Ctx, c, "vacuum_meta", 4)
		vacuumFreezeBound(t, p, b, c, "vacuum_meta")
		vacuumRows(t, p, b, c, "vacuum_meta", 1, 3)
		vacuumMaterializedMetadata(t, p, b, c)
		// Flush the independently known fourth base INSERT before TRUNCATE:
		// PG's truncate report resets shared counters but then adds pending
		// tuples_inserted from that backend's batch, even if predating truncate.
		if b.PG {
			if _, err := c.ExecContext(p.Ctx, "SELECT pg_stat_force_next_flush()"); err != nil {
				t.Fatal(err)
			}
		}
		vacuumStatistic(t, p.Ctx, c, "vacuum_meta", "n_tup_ins", 4)
		vacuumStatistic(t, p.Ctx, c, "vacuum_meta", "n_mod_since_analyze", 5)
		vacuumStatistic(t, p.Ctx, c, "vacuum_meta", "n_ins_since_vacuum", 1)
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("TRUNCATE vacuum_meta", 0))
		run(fixture.Exec("ROLLBACK", 0))
		vacuumRows(t, p, b, c, "vacuum_meta", 1, 3, 4)
		vacuumCount(t, p.Ctx, c, "vacuum_meta", 4)
		var lastBefore, lastAfter time.Time
		lastQuery := "SELECT last_vacuum FROM pg_stat_user_tables WHERE relid='vacuum_meta'::regclass"
		if err := c.QueryRowContext(p.Ctx, lastQuery).Scan(&lastBefore); err != nil {
			t.Fatal(err)
		}
		// Released committed-table TRUNCATE installs a fresh relfilenumber with
		// unknown(-1) estimate; it is neither stale last-VACUUM2 nor inferred0.
		run(fixture.Exec("TRUNCATE vacuum_meta", 0))
		if b.PG {
			if _, err := c.ExecContext(p.Ctx, "SELECT pg_stat_force_next_flush()"); err != nil {
				t.Fatal(err)
			}
		}
		for _, column := range []string{"n_live_tup", "n_dead_tup", "n_ins_since_vacuum"} {
			vacuumStatistic(t, p.Ctx, c, "vacuum_meta", column, 0)
		}
		vacuumStatistic(t, p.Ctx, c, "vacuum_meta", "n_tup_ins", 4)
		vacuumStatistic(t, p.Ctx, c, "vacuum_meta", "n_tup_del", 1)
		vacuumStatistic(t, p.Ctx, c, "vacuum_meta", "n_mod_since_analyze", 5)
		if err := c.QueryRowContext(p.Ctx, lastQuery).Scan(&lastAfter); err != nil {
			t.Fatal(err)
		}
		if !lastAfter.Equal(lastBefore) {
			t.Fatalf("TRUNCATE changed vacuum timestamp %v -> %v", lastBefore, lastAfter)
		}
		estimate := fixture.Select("SELECT reltuples FROM pg_class WHERE oid='vacuum_meta'::regclass", []string{"reltuples"}, []string{"FLOAT4"}, [][]any{{float64(-1)}})
		run(estimate)
		vacuumRun(t, p, b, vacuumHeld(t, p, c, estimate.Query), estimate)
		vacuumCount(t, p.Ctx, c, "vacuum_meta", 4)
		run(fixture.Exec("VACUUM vacuum_meta", 0))
		vacuumCount(t, p.Ctx, c, "vacuum_meta", 5)
		run(fixture.Select(estimate.Query, []string{"reltuples"}, []string{"FLOAT4"}, [][]any{{float64(0)}}))
		run(fixture.Exec("INSERT INTO vacuum_meta VALUES(7)", 1))
		vacuumRows(t, p, b, c, "vacuum_meta", 7)
		run(fixture.Exec("VACUUM vacuum_meta", 0))
		vacuumCount(t, p.Ctx, c, "vacuum_meta", 6)
		run(fixture.Select(estimate.Query, []string{"reltuples"}, []string{"FLOAT4"}, [][]any{{float64(1)}}))
	})
}

func vacuumFreezeBound(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, table string) {
	t.Helper()
	var rel, db int64
	if err := c.QueryRowContext(p.Ctx, "SELECT relfrozenxid::text::bigint FROM pg_class WHERE oid=$1::regclass", table).Scan(&rel); err != nil {
		t.Fatal(err)
	}
	if err := c.QueryRowContext(p.Ctx, "SELECT datfrozenxid::text::bigint FROM pg_database WHERE datname=current_database()").Scan(&db); err != nil {
		t.Fatal(err)
	}
	if rel < 3 || rel > 4294967295 || db < 3 || db > 4294967295 {
		t.Fatalf("invalid actual XID horizons relation=%d database=%d", rel, db)
	}
	vacuumRun(t, p, b, c, fixture.Select("SELECT age(relfrozenxid)>=0 AS valid FROM pg_class WHERE oid=$1::regclass", []string{"valid"}, []string{"BOOL"}, [][]any{{true}}, table))
	var pages, visible, frozen, mx int64
	if err := c.QueryRowContext(p.Ctx, "SELECT relpages,relallvisible,relallfrozen,relminmxid::text::bigint FROM pg_class WHERE oid=$1::regclass", table).Scan(&pages, &visible, &frozen, &mx); err != nil {
		t.Fatal(err)
	}
	if b.PG {
		if pages < 0 || visible < 0 || frozen < 0 || visible > pages || frozen > visible || mx < 1 {
			t.Fatalf("invalid physical metadata %d %d %d %d", pages, visible, frozen, mx)
		}
	} else if pages != 0 || visible != 0 || frozen != 0 || mx != 0 {
		t.Fatalf("native fabricated physical metadata %d %d %d %d", pages, visible, frozen, mx)
	}
}

func vacuumMaterializedMetadata(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { vacuumRun(t, p, b, c, s) }
	run(fixture.Exec("CREATE MATERIALIZED VIEW vacuum_meta_mv AS SELECT id FROM vacuum_meta", 2))
	run(fixture.Exec("VACUUM vacuum_meta_mv", 0))
	vacuumCount(t, p.Ctx, c, "vacuum_meta_mv", 1)
	var before, after time.Time
	stamp := "SELECT last_vacuum FROM pg_stat_user_tables WHERE relid='vacuum_meta_mv'::regclass"
	if err := c.QueryRowContext(p.Ctx, stamp).Scan(&before); err != nil {
		t.Fatal(err)
	}
	run(fixture.Exec("INSERT INTO vacuum_meta VALUES(4)", 1))
	refresh := "REFRESH MATERIALIZED VIEW vacuum_meta_mv"
	vacuumRun(t, p, b, vacuumHeld(t, p, c, refresh), fixture.Exec(refresh, 0))
	vacuumCount(t, p.Ctx, c, "vacuum_meta_mv", 1)
	if err := c.QueryRowContext(p.Ctx, stamp).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if !after.Equal(before) {
		t.Fatalf("same-object refresh changed vacuum timestamp %v -> %v", before, after)
	}
	vacuumRows(t, p, b, c, "vacuum_meta_mv", 1, 3, 4)
	run(fixture.Exec("VACUUM vacuum_meta_mv", 0))
	vacuumCount(t, p.Ctx, c, "vacuum_meta_mv", 2)
	run(fixture.Select("SELECT reltuples FROM pg_class WHERE oid='vacuum_meta_mv'::regclass", []string{"reltuples"}, []string{"FLOAT4"}, [][]any{{float64(3)}}))
}
