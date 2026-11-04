package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGStatAllTablesEventsAndRollback(t *testing.T) {
	tableStatEventsAndRollback(t, tableStatFamily{})
}

func tableStatEventsAndRollback(t *testing.T, f tableStatFamily) {
	f.scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
		for _, end := range []string{"ROLLBACK", "COMMIT"} {
			name := "sat_abort"
			if end == "COMMIT" {
				name = "sat_commit"
			}
			f.create(t, p, b, c, name)
			// Keep BEGIN as the reporter's first command, as in the xact
			// family: pre-transaction setup would establish a flush throttle.
			w, retire := fixture.StatisticsReportingConn(t, p, b)
			target := name
			if f.system {
				target = "information_schema." + name
			}
			fixture.StatisticsExec(t, p, b, w, "BEGIN", 0)
			fixture.StatisticsExec(t, p, b, w, "INSERT INTO "+target+" VALUES(1,'a'),(2,NULL)", 2)
			if r := f.get(t, p, c, name); r.Inserts != 0 || r.Updates != 0 || r.Deletes != 0 {
				t.Fatal("unresolved cumulative ownership", r)
			}
			if f.system {
				xactTableCounts(t, xactSysGet(t, p, w, name), 2, 0, 0)
				xactTableCounts(t, xactSysGet(t, p, c, name), 0, 0, 0)
				if cut := f.get(t, p, w, name); cut.Inserts != 0 {
					t.Fatal("owner unresolved cumulative work", cut)
				}
			}
			if f.system {
				xactTableEnd(t, p, b, w, end)
				fixture.StatisticsExec(t, p, b, w, "BEGIN", 0)
				xactTableCounts(t, xactSysGet(t, p, w, name), 0, 0, 0)
				fixture.StatisticsExec(t, p, b, w, "ROLLBACK", 0)
			} else {
				fixture.StatisticsExec(t, p, b, w, end, 0)
			}
			retire()
			r := f.await(t, p, b, name, "settled inserts2", func(r tableStatRow) bool { return r.Inserts == 2 })
			if r.Updates != 0 || r.Deletes != 0 || r.Since != 2 {
				t.Fatal("NULL row-event settlement", r)
			}
			live, dead, mods, count := int64(0), int64(2), int64(0), int64(0)
			if end == "COMMIT" {
				live, dead, mods, count = 2, 0, 2, 2
			}
			if r.Mods != mods {
				t.Fatal("modification settlement", r, mods)
			}
			if !b.PG && (r.Live != live || r.Dead != dead || r.HOT != 0 || r.Newpage != 0) {
				t.Fatal("declared native version estimates", r, live, dead)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM "+name, []string{"count"}, []string{"INT8"}, [][]any{{count}}))
		}
		f.create(t, p, b, c, "sat_vector")
		f.seed(t, p, b, "sat_vector")
		w, retire := f.reporting(t, p, b)
		fixture.StatisticsExec(t, p, b, w, "UPDATE sat_vector SET payload=payload WHERE n=1", 1)
		fixture.StatisticsExec(t, p, b, w, "DELETE FROM sat_vector WHERE n=3", 1)
		fixture.StatisticsExec(t, p, b, w, "UPDATE sat_vector SET payload='absent' WHERE n=99", 0)
		fixture.StatisticsExec(t, p, b, w, "DELETE FROM sat_vector WHERE n=99", 0)
		stmt, err := w.PrepareContext(p.Ctx, "INSERT INTO sat_vector VALUES($1,$2)")
		if err != nil {
			t.Fatal(err)
		}
		stmt.Close()
		retire()
		r := f.await(t, p, b, "sat_vector", "I3/U1/D1", func(r tableStatRow) bool { return r.Inserts == 3 && r.Updates == 1 && r.Deletes == 1 })
		if r.Mods != 5 || r.Since != 3 {
			t.Fatal("exact modification counters", r)
		}
		if !b.PG && (r.Live != 2 || r.Dead != 2 || r.HOT != 0 || r.Newpage != 0) {
			t.Fatal("logical native tuple-version estimates", r)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT n,payload FROM sat_vector ORDER BY n", []string{"n", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), nil}}))
	})
}

func TestReferencePGStatAllTablesMaintenance(t *testing.T) {
	tableStatMaintenance(t, tableStatFamily{})
}

func tableStatMaintenance(t *testing.T, f tableStatFamily) {
	f.scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		f.create(t, p, b, c, "sat_maintenance")
		fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
		w, retire := f.reporting(t, p, b)
		fixture.StatisticsExec(t, p, b, w, "INSERT INTO sat_maintenance VALUES(1,'a'),(2,NULL)", 2)
		retire()
		before := f.await(t, p, b, "sat_maintenance", "I2/Mods2/Since2", func(r tableStatRow) bool { return r.Inserts == 2 })
		if before.Analyze != 0 || before.Vacuum != 0 || before.LastAnalyze.Valid || before.LastVacuum.Valid || before.Mods != 2 || before.Since != 2 {
			t.Fatal("maintenance baseline", before)
		}
		prepare, err := c.PrepareContext(p.Ctx, "ANALYZE sat_maintenance")
		if err != nil {
			t.Fatal(err)
		}
		prepare.Close()
		if got := f.get(t, p, c, "sat_maintenance"); !got.equal(before) {
			t.Fatal("prepare published maintenance", got, before)
		}
		fixture.StatisticsExec(t, p, b, c, "ANALYZE sat_maintenance(n)", 0)
		column := f.await(t, p, b, "sat_maintenance", "column ANALYZE1", func(r tableStatRow) bool { return r.Analyze == 1 })
		if column.Mods != 2 || !column.LastAnalyze.Valid || column.Inserts != 2 || column.Since != 2 {
			t.Fatal("column ANALYZE reset relation modifications", column)
		}
		fixture.StatisticsExec(t, p, b, c, "ANALYZE sat_maintenance", 0)
		analyzed := f.await(t, p, b, "sat_maintenance", "full ANALYZE2", func(r tableStatRow) bool { return r.Analyze == 2 })
		if analyzed.Mods != 0 || analyzed.Inserts != 2 || analyzed.Since != 2 || !analyzed.LastAnalyze.Valid || analyzed.AnalyzeTime < column.AnalyzeTime {
			t.Fatal("full ANALYZE completion", analyzed)
		}
		for range 2 {
			if got := f.get(t, p, c, "sat_maintenance"); !got.equal(analyzed) {
				t.Fatal("statistics SELECT changed completed facts", analyzed, got)
			}
		}
		fixture.StatisticsExec(t, p, b, c, "VACUUM sat_maintenance", 0)
		vacuumed := f.await(t, p, b, "sat_maintenance", "ordinary VACUUM1", func(r tableStatRow) bool { return r.Vacuum == 1 })
		if vacuumed.Since != 0 || vacuumed.Inserts != 2 || !vacuumed.LastVacuum.Valid || vacuumed.Analyze != 2 || !vacuumed.LastAnalyze.Time.Equal(analyzed.LastAnalyze.Time) || vacuumed.AnalyzeTime != analyzed.AnalyzeTime {
			t.Fatal("source-specific VACUUM completion", vacuumed)
		}
		if vacuumed.Seq != 0 || vacuumed.Read != 0 || vacuumed.LastSeq.Valid {
			t.Fatal("ANALYZE/lazy VACUUM fabricated sequential source events", vacuumed)
		}
		tableStatAutomatic(t, vacuumed)
		for range 2 {
			if got := f.get(t, p, c, "sat_maintenance"); !got.equal(vacuumed) {
				t.Fatal("repeated ALL SELECT advanced maintenance", got)
			}
		}
		stable, err := c.PrepareContext(p.Ctx, f.query())
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			var got tableStatRow
			if err := stable.QueryRowContext(p.Ctx, "sat_maintenance").Scan(got.destinations()...); err != nil {
				t.Fatal(err)
			}
			if !got.equal(vacuumed) {
				t.Fatal("prepared statistics read changed maintenance", got, vacuumed)
			}
		}
		stable.Close()
		fixture.StatisticsExec(t, p, b, c, "SET track_counts=off", 0)
		fixture.StatisticsExec(t, p, b, c, "ANALYZE sat_maintenance", 0)
		fixture.StatisticsExec(t, p, b, c, "VACUUM sat_maintenance", 0)
		if got := f.get(t, p, c, "sat_maintenance"); !got.equal(vacuumed) {
			t.Fatal("disabled maintenance published facts", got, vacuumed)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM sat_maintenance", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
		fixture.StatisticsExec(t, p, b, c, "SET track_counts=on", 0)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic("ANALYZE sat_missing", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `relation "sat_missing" does not exist`}))
		// Pool acquisition rejects this before driver admission. A held pq
		// connection can complete a quick command despite a pre-canceled context.
		cancelled, cancel := context.WithCancel(p.Ctx)
		cancel()
		if _, err := b.DB.ExecContext(cancelled, "VACUUM sat_maintenance"); !errors.Is(err, context.Canceled) {
			t.Fatal("pre-admission maintenance cancellation", err)
		}
		if got := f.get(t, p, c, "sat_maintenance"); !got.equal(vacuumed) {
			t.Fatal("rejected/canceled maintenance completed", got)
		}
		fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
		fixture.StatisticsExec(t, p, b, c, "ANALYZE sat_maintenance", 0)
		fixture.StatisticsExec(t, p, b, c, "ROLLBACK", 0)
		rolled := f.await(t, p, b, "sat_maintenance", "existing-object ANALYZE survives rollback", func(r tableStatRow) bool { return r.Analyze == 3 })
		if rolled.Vacuum != 1 || !rolled.LastAnalyze.Valid || rolled.AnalyzeTime < analyzed.AnalyzeTime {
			t.Fatal("completed maintenance rolled back", rolled)
		}
		full, retireFull := f.reporting(t, p, b)
		fixture.StatisticsExec(t, p, b, full, "VACUUM FULL sat_maintenance", 0)
		retireFull()
		rewritten := f.await(t, p, b, "sat_maintenance", "actual FULL source1/read2", func(r tableStatRow) bool { return r.Seq == 1 && r.Read == 2 })
		if rewritten.Vacuum != 1 || !rewritten.LastVacuum.Time.Equal(rolled.LastVacuum.Time) || rewritten.VacuumTime != rolled.VacuumTime || rewritten.Inserts != 2 || rewritten.Analyze != 3 {
			t.Fatal("FULL rewrite counted ordinary maintenance or tuple work", rewritten, rolled)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT n,payload FROM sat_maintenance ORDER BY n", []string{"n", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), nil}}))
	})
}
