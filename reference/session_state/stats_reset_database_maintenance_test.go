package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

type resetDatabaseMaintenance struct {
	Vacuum, Analyze, AutoVacuum, AutoAnalyze                 int64
	LastVacuum, LastAnalyze, LastAutoVacuum, LastAutoAnalyze sql.NullTime
	VacuumTime, AnalyzeTime, AutoVacuumTime, AutoAnalyzeTime float64
	SeqScans, SeqTuples                                      int64
	LastSeqScan                                              sql.NullTime
}

func resetDatabaseMaintenanceRead(t *testing.T, p *fixture.Pair, c *sql.Conn, table string) resetDatabaseMaintenance {
	t.Helper()
	var r resetDatabaseMaintenance
	if err := c.QueryRowContext(p.Ctx, "SELECT vacuum_count,analyze_count,autovacuum_count,autoanalyze_count,last_vacuum,last_analyze,last_autovacuum,last_autoanalyze,total_vacuum_time,total_analyze_time,total_autovacuum_time,total_autoanalyze_time,seq_scan,seq_tup_read,last_seq_scan FROM pg_stat_user_tables WHERE relname=$1", table).Scan(&r.Vacuum, &r.Analyze, &r.AutoVacuum, &r.AutoAnalyze, &r.LastVacuum, &r.LastAnalyze, &r.LastAutoVacuum, &r.LastAutoAnalyze, &r.VacuumTime, &r.AnalyzeTime, &r.AutoVacuumTime, &r.AutoAnalyzeTime, &r.SeqScans, &r.SeqTuples, &r.LastSeqScan); err != nil {
		t.Fatal(err)
	}
	return r
}

type resetDatabaseOptimizer struct {
	Pages, Visible, Frozen, Width  int64
	Tuples, NullFraction, Distinct float64
	Common, Frequencies, Histogram sql.NullString
	Correlation                    sql.NullFloat64
}

func resetDatabaseOptimizerRead(t *testing.T, p *fixture.Pair, c *sql.Conn, table string) resetDatabaseOptimizer {
	t.Helper()
	var r resetDatabaseOptimizer
	if err := c.QueryRowContext(p.Ctx, "SELECT relpages,reltuples,relallvisible,relallfrozen FROM pg_class WHERE oid=$1::regclass", table).Scan(&r.Pages, &r.Tuples, &r.Visible, &r.Frozen); err != nil {
		t.Fatal(err)
	}
	if err := c.QueryRowContext(p.Ctx, "SELECT null_frac,avg_width,n_distinct,most_common_vals::text,most_common_freqs::text,histogram_bounds::text,correlation FROM pg_stats WHERE schemaname='public' AND tablename=$1 AND attname='id' AND NOT inherited", table).Scan(&r.NullFraction, &r.Width, &r.Distinct, &r.Common, &r.Frequencies, &r.Histogram, &r.Correlation); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestReferenceStatsResetDatabaseMaintenance(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		const table = "reset_database_maintenance"
		statsCreate(t, p, b, c, table)
		activityExec(t, p, c, "SET stats_fetch_consistency=none")
		statsPublish(t, p, b, table, 3, fixture.Exec("INSERT INTO "+table+" VALUES(1),(2),(3)", 3))
		writer, retire := statsReportingConn(t, p, b)
		statsExec(t, p, b, writer, "VACUUM "+table, 0)
		statsExec(t, p, b, writer, "ANALYZE "+table, 0)
		statsExec(t, p, b, writer, "INSERT INTO "+table+" VALUES(4),(5)", 2)
		statsExec(t, p, b, writer, "UPDATE "+table+" SET id=11 WHERE id=1", 1)
		statsExec(t, p, b, writer, "DELETE FROM "+table+" WHERE id=2", 1)
		retire()
		// Full ANALYZE resets modifications; subsequent I2/U1/D1 contributes
		// four modifications, two live inserts and two dead logical versions.
		positive := statsReportingVector{5, 1, 1, 4, 2, 4, 2}
		statsReportingAwait(t, p, c, table, positive)
		statsReportingWant(t, p, b, c, table, positive)
		maintenance := resetDatabaseMaintenanceRead(t, p, c, table)
		if maintenance.Vacuum != 1 || maintenance.Analyze != 1 || !maintenance.LastVacuum.Valid || !maintenance.LastAnalyze.Valid || maintenance.AutoVacuum != 0 || maintenance.AutoAnalyze != 0 || maintenance.LastAutoVacuum.Valid || maintenance.LastAutoAnalyze.Valid {
			t.Fatal("genuine pre-reset maintenance/history", maintenance)
		}
		if maintenance.SeqScans <= 0 || maintenance.SeqTuples <= 0 || !maintenance.LastSeqScan.Valid {
			t.Fatal("genuine published scan history absent", maintenance)
		}
		optimizer := resetDatabaseOptimizerRead(t, p, c, table)
		if optimizer.Tuples != 3 || optimizer.Width <= 0 || optimizer.Distinct == 0 {
			t.Fatal("genuine retained optimizer statistics absent", optimizer)
		}
		statsReportingRows(t, p, b, c, table, []int64{3, 4, 5, 11})
		// Keep this observer's own access/command deltas pending while observing
		// the published reset cut, rather than assuming a throttle interval.
		activityExec(t, p, c, "BEGIN")
		resetDatabaseValue(t, p, b, c, "SELECT pg_stat_reset()", "pg_stat_reset", "")
		statsReportingWant(t, p, b, c, table, statsReportingVector{})
		zero := resetDatabaseMaintenanceRead(t, p, c, table)
		if zero.Vacuum != 0 || zero.Analyze != 0 || zero.AutoVacuum != 0 || zero.AutoAnalyze != 0 || zero.LastVacuum.Valid || zero.LastAnalyze.Valid || zero.LastAutoVacuum.Valid || zero.LastAutoAnalyze.Valid || zero.VacuumTime != 0 || zero.AnalyzeTime != 0 || zero.AutoVacuumTime != 0 || zero.AutoAnalyzeTime != 0 {
			t.Fatal("database reset retained maintenance/autovacuum state", zero)
		}
		if zero.SeqScans != 0 || zero.SeqTuples != 0 || zero.LastSeqScan.Valid {
			t.Fatal("database reset retained scan history", zero)
		}
		if got := resetDatabaseOptimizerRead(t, p, c, table); got != optimizer {
			t.Fatal("statistics reset changed optimizer data", got, optimizer)
		}
		statsReportingRows(t, p, b, c, table, []int64{3, 4, 5, 11})
		activityExec(t, p, c, "ROLLBACK")
		// A new genuine maintenance event starts a new cumulative history while
		// updating the optimizer's previously retained three-row estimate.
		analyzer, retireAnalyzer := statsReportingConn(t, p, b)
		statsExec(t, p, b, analyzer, "ANALYZE "+table, 0)
		retireAnalyzer()
		statsAwait(t, p, b, table, "analyze_count", 1)
		// ANALYZE samples retained physical state, not the zeroed cumulative
		// history. Native logical storage adds one dead estimate for DELETE;
		// PostgreSQL's heap retains both the UPDATE and DELETE dead versions.
		dead := int64(1)
		if b.PG {
			dead = 2
		}
		statsReportingWant(t, p, b, c, table, statsReportingVector{0, 0, 0, 4, dead, 0, 0})
		after := resetDatabaseMaintenanceRead(t, p, c, table)
		if after.Analyze != 1 || !after.LastAnalyze.Valid || after.Vacuum != 0 || after.LastVacuum.Valid || after.AutoVacuum != 0 || after.AutoAnalyze != 0 || after.LastAutoVacuum.Valid || after.LastAutoAnalyze.Valid {
			t.Fatal("new maintenance history", after)
		}
		if got := resetDatabaseOptimizerRead(t, p, c, table); got.Tuples != 4 {
			t.Fatal("new ANALYZE did not update actual row estimate", got)
		}
	})
}
