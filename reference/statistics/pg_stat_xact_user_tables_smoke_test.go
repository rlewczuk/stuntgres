package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatXactUserTablesFeatureSmoke(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xactUserCreate(t, p, b, c, "xut_smoke")
		fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
		for _, view := range []string{"pg_stat_xact_all_tables", "pg_stat_xact_user_tables", "pg_stat_xact_sys_tables"} {
			want := int64(1)
			if view == "pg_stat_xact_sys_tables" { want = 0 }
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM "+view+" WHERE relname='xut_smoke'", []string{"count"}, []string{"INT8"}, [][]any{{want}}))
		}
		maintenance := tableStatGet(t, p, c, "xut_smoke")
		if maintenance.Vacuum != 0 || maintenance.Analyze != 0 || maintenance.LastVacuum.Valid || maintenance.LastAnalyze.Valid { t.Fatal("fresh maintenance baseline", maintenance) }
		w, retire := fixture.StatisticsReportingConn(t, p, b)
		fixture.StatisticsExec(t, p, b, w, "BEGIN", 0)
		tableStatSeqPrefer(t, p, b, w)
		stmt, err := w.PrepareContext(p.Ctx, xactUserSelect)
		if err != nil { t.Fatal(err) }
		defer stmt.Close()
		var metadata []*sql.ColumnType
		for _, prepared := range []bool{false, true} {
			var rows *sql.Rows
			if prepared { rows, err = stmt.QueryContext(p.Ctx, "xut_smoke") } else { rows, err = w.QueryContext(p.Ctx, xactUserSelect, "xut_smoke") }
			if err != nil { t.Fatal(err) }
			metadata, err = rows.ColumnTypes()
			if err != nil { t.Fatal(err) }
			xactTableMetadata(t, metadata, b.PG)
			baseline := xactTableBatchRow(t, rows, b.PG)
			rows.Close()
			xactTableCounts(t, baseline, 0, 0, 0)
			if baseline.Index.Valid || baseline.Fetch.Valid { t.Fatal("no-index typed NULLs", baseline) }
			xactTableMetadata(t, metadata, b.PG)
		}
		fixture.StatisticsExec(t, p, b, w, "INSERT INTO public.xut_smoke VALUES(1,NULL),(2,'b')", 2)
		old := xactUserGet(t, p, w, "xut_smoke")
		xactTableCounts(t, old, 2, 0, 0)
		var held xactTableRow
		if err := stmt.QueryRowContext(p.Ctx, "xut_smoke").Scan(held.destinations()...); err != nil { t.Fatal(err) }
		xactTableCounts(t, held, 2, 0, 0)
		xactTableCounts(t, xactTableGet(t, p, w, "xut_smoke"), 2, 0, 0)
		xactTableCounts(t, xactUserGet(t, p, c, "xut_smoke"), 0, 0, 0)
		const scanSQL = "SELECT n,payload FROM public.xut_smoke ORDER BY n"
		method := indexStatPlan(t, p, w, scanSQL)
		if b.PG {
			tableStatSeqPlan(t, b, method)
		} else if !strings.Contains(method, "Logical Scan on xut_smoke") {
			t.Fatal("actual native sequential scan missing", method)
		}
		fixture.RunStep(t, p.Ctx, w, b.Recorder, fixture.Select(scanSQL, []string{"n", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), nil}, {int64(2), "b"}}))
		scanned := xactUserGet(t, p, w, "xut_smoke")
		xactTableCounts(t, scanned, 2, 0, 0)
		if scanned.Seq != 1 || scanned.Read != 2 { t.Fatal("authenticated USER scan1/read2", scanned) }
		if held != old { t.Fatal("detached event values changed", held, old) }
		stmt.Close()
		xactTableEnd(t, p, b, w, "ROLLBACK")
		fixture.StatisticsExec(t, p, b, w, "BEGIN", 0)
		zero := xactUserGet(t, p, w, "xut_smoke")
		xactTableCounts(t, zero, 0, 0, 0)
		if zero.Seq != 0 || zero.Read != 0 { t.Fatal("controlled terminal publication retained local scans", zero) }
		fixture.StatisticsExec(t, p, b, w, "ROLLBACK", 0)
		retire()
		published := tableStatAwait(t, p, b, "xut_smoke", "rollback publishes executed inserts2", func(r tableStatRow) bool { return r.Inserts == 2 })
		if published.Updates != 0 || published.Deletes != 0 { t.Fatal("unexpected cumulative tuple vector", published) }
		fixture.StatisticsExec(t, p, b, c, "SET track_counts=off", 0)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM public.xut_smoke", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic("UPDATE pg_stat_xact_user_tables SET seq_scan=9 WHERE FALSE", xactTableDiagnostic("UPDATE")))
		before := tableStatGet(t, p, c, "xut_smoke")
		if before.Vacuum != 0 || before.Analyze != 0 || before.LastVacuum.Valid || before.LastAnalyze.Valid { t.Fatal("non-maintenance work fabricated completion", before) }
		for range 2 {
			xactUserGet(t, p, c, "xut_smoke")
			var r xactTableRow
			s, err := c.PrepareContext(p.Ctx, xactUserSelect)
			if err != nil { t.Fatal(err) }
			if err := s.QueryRowContext(p.Ctx, "xut_smoke").Scan(r.destinations()...); err != nil { t.Fatal(err) }
			s.Close()
			if got := tableStatGet(t, p, c, "xut_smoke"); !got.equal(before) { t.Fatal("USER read changed maintenance", got, before) }
		}
		xactTableMetadata(t, metadata, b.PG)
		xactTableCounts(t, old, 2, 0, 0)
	})
}
