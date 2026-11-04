package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatXactSysTablesFeatureSmoke(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xactSysCreate(t, p, b, c, "xst_smoke")
		tableStatCreate(t, p, b, c, "xst_smoke_public")
		xactSysCreate(t, p, b, c, "xst_smoke_scan")
		seed, retireSeed := fixture.StatisticsReportingConn(t, p, b)
		fixture.StatisticsExec(t, p, b, seed, "INSERT INTO information_schema.xst_smoke_scan VALUES(10,NULL),(20,'scan')", 2)
		retireSeed()
		tableStatAwait(t, p, b, "xst_smoke_scan", "published scan fixture inserts2", func(r tableStatRow) bool { return r.Inserts == 2 })
		fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
		if r := xactSysGet(t, p, c, "pg_class"); r.ID != 1259 || r.Schema != "pg_catalog" {
			t.Fatal("represented system membership", r)
		}
		for _, view := range []string{"pg_stat_xact_all_tables", "pg_stat_all_tables", "pg_stat_user_tables", "pg_stat_xact_sys_tables"} {
			count := int64(1)
			if view == "pg_stat_xact_sys_tables" {
				count = 0
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM "+view+" WHERE relname='xst_smoke_public'", []string{"count"}, []string{"INT8"}, [][]any{{count}}))
		}
		maintenance := tableStatGet(t, p, c, "xst_smoke")
		w, retire := fixture.StatisticsReportingConn(t, p, b)
		fixture.StatisticsExec(t, p, b, w, "BEGIN", 0)
		tableStatSeqPrefer(t, p, b, w)
		stmt, err := w.PrepareContext(p.Ctx, xactSysSelect)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := w.QueryContext(p.Ctx, xactSysSelect, "xst_smoke")
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		xactTableMetadata(t, columns, b.PG)
		if !rows.Next() {
			t.Fatal("baseline SYS absent", rows.Err())
		}
		var baseline xactTableRow
		if err := rows.Scan(baseline.destinations()...); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if baseline.Index.Valid || baseline.Fetch.Valid {
			t.Fatal("no-index must be NULL", baseline)
		}
		xactTableCounts(t, baseline, 0, 0, 0)
		fixture.StatisticsExec(t, p, b, w, "INSERT INTO information_schema.xst_smoke VALUES(1,NULL),(2,'b')", 2)
		old := xactSysGet(t, p, w, "xst_smoke")
		xactTableCounts(t, old, 2, 0, 0)
		rows, err = stmt.QueryContext(p.Ctx, "xst_smoke")
		if err != nil {
			t.Fatal(err)
		}
		held := xactTableBatchRow(t, rows, b.PG)
		rows.Close()
		stmt.Close()
		xactTableCounts(t, held, 2, 0, 0)
		xactTableCounts(t, xactSysGet(t, p, c, "xst_smoke"), 0, 0, 0)
		scanQuery := "SELECT payload FROM information_schema.xst_smoke_scan WHERE n=10"
		tableStatSeqPlan(t, b, indexStatPlan(t, p, w, scanQuery))
		fixture.RunStep(t, p.Ctx, w, b.Recorder, fixture.Select(scanQuery, []string{"payload"}, []string{"TEXT"}, [][]any{{nil}}))
		scanned := xactSysGet(t, p, w, "xst_smoke_scan")
		if scanned.Seq != 1 || scanned.Read != 2 {
			t.Fatal("actual published SYS scan1/read2", scanned)
		}
		xactTableCounts(t, scanned, 0, 0, 0)
		xactTableCounts(t, xactSysGet(t, p, w, "xst_smoke"), 2, 0, 0)
		if old != held {
			t.Fatal("retained local rows mutated", old, held)
		}
		xactTableEnd(t, p, b, w, "ROLLBACK")
		fixture.StatisticsExec(t, p, b, w, "BEGIN", 0)
		xactTableCounts(t, xactSysGet(t, p, w, "xst_smoke"), 0, 0, 0)
		fixture.StatisticsExec(t, p, b, w, "ROLLBACK", 0)
		retire()
		fixture.StatisticsExec(t, p, b, c, "SET track_counts=off", 0)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM information_schema.xst_smoke", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		published := tableStatAwait(t, p, b, "xst_smoke", "rollback publishes executed2", func(r tableStatRow) bool { return r.Inserts == 2 })
		sys := xactSysCumulative(t, p, c, "xst_smoke")
		if sys.Inserts != 2 || published.Updates != 0 || published.Deletes != 0 {
			t.Fatal("executed events not committed rows", sys, published)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic("UPDATE pg_stat_xact_sys_tables SET seq_scan=9 WHERE FALSE", xactTableDiagnostic("UPDATE")))
		before := tableStatGet(t, p, c, "xst_smoke")
		if before.Vacuum != maintenance.Vacuum || before.Analyze != maintenance.Analyze || before.LastVacuum.Valid != maintenance.LastVacuum.Valid || before.LastAnalyze.Valid != maintenance.LastAnalyze.Valid {
			t.Fatal("non-maintenance work fabricated completion", before, maintenance)
		}
		for range 2 {
			xactSysGet(t, p, c, "xst_smoke")
			if got := tableStatGet(t, p, c, "xst_smoke"); !got.equal(before) {
				t.Fatal("SYS read changed maintenance", got, before)
			}
		}
		xactTableMetadata(t, columns, b.PG)
		xactTableCounts(t, old, 2, 0, 0)
	})
}
