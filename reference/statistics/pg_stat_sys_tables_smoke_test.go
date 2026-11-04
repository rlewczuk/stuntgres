package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatSysTablesFeatureSmoke(t *testing.T) {
	f := tableStatFamily{system: true}
	f.scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
		f.create(t, p, b, c, "sst_smoke")
		f.create(t, p, b, c, "sst_smoke_scan")
		f.seed(t, p, b, "sst_smoke_scan")
		tableStatCreate(t, p, b, c, "public.sst_smoke_public")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pg_stat_sys_tables WHERE relname='sst_smoke_public'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		initial := f.get(t, p, c, "sst_smoke")
		if initial.ID <= 0 || initial.Schema != "information_schema" || initial.Name != "sst_smoke" || initial.Inserts != 0 || initial.Seq != 0 || initial.Read != 0 || initial.Index.Valid || initial.Fetch.Valid || initial.LastIndex.Valid || initial.LastSeq.Valid || initial.LastVacuum.Valid || initial.LastAnalyze.Valid {
			t.Fatal("initial SYS contract", initial)
		}
		tableStatAutomatic(t, initial)
		holder := f.connection(t, p, b)
		held, err := holder.QueryContext(p.Ctx, f.query(), "sst_smoke")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		columns, err := held.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		tableStatMetadata(t, columns, b.PG)
		// BEGIN must precede any reporter setup that establishes a flush throttle.
		w, retire := fixture.StatisticsReportingConn(t, p, b)
		fixture.StatisticsExec(t, p, b, w, "BEGIN", 0)
		fixture.StatisticsExec(t, p, b, w, "INSERT INTO information_schema.sst_smoke VALUES(1,NULL),(2,'b')", 2)
		xactTableCounts(t, xactSysGet(t, p, w, "sst_smoke"), 2, 0, 0)
		xactTableCounts(t, xactSysGet(t, p, c, "sst_smoke"), 0, 0, 0)
		if r := f.get(t, p, c, "sst_smoke"); r.Inserts != 0 {
			t.Fatal("open transaction published", r)
		}
		xactTableEnd(t, p, b, w, "ROLLBACK")
		fixture.StatisticsExec(t, p, b, w, "BEGIN", 0)
		xactTableCounts(t, xactSysGet(t, p, w, "sst_smoke"), 0, 0, 0)
		fixture.StatisticsExec(t, p, b, w, "ROLLBACK", 0)
		retire()
		aborted := f.await(t, p, b, "sst_smoke", "aborted I2", func(r tableStatRow) bool { return r.Inserts == 2 })
		if aborted.Updates != 0 || aborted.Deletes != 0 || aborted.Mods != 0 || aborted.Since != 2 {
			t.Fatal("executed NULL-row settlement", aborted)
		}
		fixture.StatisticsExec(t, p, b, c, "SET track_counts=off", 0)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM sst_smoke", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		fixture.StatisticsExec(t, p, b, c, "SET track_counts=on", 0)
		reader, retireReader := f.reporting(t, p, b)
		tableStatSeqPrefer(t, p, b, reader)
		q := "SELECT payload FROM sst_smoke_scan WHERE n=$1"
		tableStatSeqPlan(t, b, indexStatPlan(t, p, reader, q, 2))
		fixture.RunStep(t, p.Ctx, reader, b.Recorder, fixture.Select(q, []string{"payload"}, []string{"TEXT"}, [][]any{{nil}}, 2))
		stmt, err := reader.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: reader, Statement: stmt, SQL: q}, b.Recorder, fixture.Select(q, []string{"payload"}, []string{"TEXT"}, nil, 99))
		stmt.Close()
		retireReader()
		scanned := f.await(t, p, b, "sst_smoke_scan", "direct hit/prepared miss scan2/read6", func(r tableStatRow) bool { return r.Seq == 2 && r.Read == 6 })
		if !scanned.LastSeq.Valid || scanned.Inserts != 3 {
			t.Fatal("actual sequential activity", scanned)
		}
		fixture.StatisticsExec(t, p, b, c, "ANALYZE sst_smoke", 0)
		analyzed := f.await(t, p, b, "sst_smoke", "completed ANALYZE1", func(r tableStatRow) bool { return r.Analyze == 1 })
		if !analyzed.LastAnalyze.Valid || analyzed.Mods != 0 || analyzed.Inserts != 2 {
			t.Fatal("ANALYZE completion", analyzed)
		}
		fixture.StatisticsExec(t, p, b, c, "VACUUM sst_smoke", 0)
		maintained := f.await(t, p, b, "sst_smoke", "completed VACUUM1", func(r tableStatRow) bool { return r.Vacuum == 1 })
		if !maintained.LastVacuum.Valid || maintained.Since != 0 || maintained.Analyze != 1 || maintained.Inserts != 2 {
			t.Fatal("VACUUM completion", maintained)
		}
		tableStatAutomatic(t, maintained)
		stats, err := c.PrepareContext(p.Ctx, f.query())
		if err != nil {
			t.Fatal(err)
		}
		defer stats.Close()
		for range 2 {
			direct := f.get(t, p, c, "sst_smoke")
			var prepared tableStatRow
			if err := stats.QueryRowContext(p.Ctx, "sst_smoke").Scan(prepared.destinations()...); err != nil {
				t.Fatal(err)
			}
			if !direct.equal(maintained) || !prepared.equal(maintained) {
				t.Fatal("statistics reads changed completed history", direct, prepared, maintained)
			}
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic("UPDATE pg_stat_sys_tables SET seq_scan=0 WHERE FALSE", tableStatDiagnostic("UPDATE")))
		if got := f.get(t, p, c, "sst_smoke"); !got.equal(maintained) {
			t.Fatal("read-only error changed SYS", got, maintained)
		}
		fixture.StatisticsExec(t, p, b, c, "DROP TABLE sst_smoke", 0)
		if _, err := f.read(p.Ctx, c, "sst_smoke"); err != sql.ErrNoRows {
			t.Fatal("committed drop still present", err)
		}
		if !held.Next() {
			t.Fatal("held SYS row lost", held.Err())
		}
		var retained tableStatRow
		if err := held.Scan(retained.destinations()...); err != nil {
			t.Fatal(err)
		}
		held.Close()
		if !retained.equal(initial) {
			t.Fatal("held SYS result changed", retained, initial)
		}
		tableStatMetadata(t, columns, b.PG)
	})
}
