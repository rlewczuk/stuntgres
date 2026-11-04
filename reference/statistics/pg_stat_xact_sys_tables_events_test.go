package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatXactSysTablesTransactions(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, end := range []string{"ROLLBACK", "COMMIT"} {
			name := "xst_abort"
			if end == "COMMIT" {
				name = "xst_commit"
			}
			xactSysCreate(t, p, b, c, name)
			w, retire := fixture.StatisticsReportingConn(t, p, b)
			fixture.StatisticsExec(t, p, b, w, "BEGIN", 0)
			stmt, err := w.PrepareContext(p.Ctx, xactSysSelect)
			if err != nil {
				t.Fatal(err)
			}
			xactTableCounts(t, xactSysGet(t, p, w, name), 0, 0, 0)
			fixture.StatisticsExec(t, p, b, w, "INSERT INTO information_schema."+name+" VALUES(1,NULL),(2,'b')", 2)
			old := xactSysGet(t, p, w, name)
			xactTableCounts(t, old, 2, 0, 0)
			var held xactTableRow
			if err := stmt.QueryRowContext(p.Ctx, name).Scan(held.destinations()...); err != nil {
				t.Fatal(err)
			}
			stmt.Close()
			xactTableCounts(t, held, 2, 0, 0)
			xactTableCounts(t, xactTableGet(t, p, w, name), 2, 0, 0)
			xactTableCounts(t, xactSysGet(t, p, c, name), 0, 0, 0)
			xactTableEnd(t, p, b, w, end)
			fixture.StatisticsExec(t, p, b, w, "BEGIN", 0)
			xactTableCounts(t, xactSysGet(t, p, w, name), 0, 0, 0)
			fixture.StatisticsExec(t, p, b, w, "ROLLBACK", 0)
			retire()
			published := tableStatAwait(t, p, b, name, "executed SYS inserts2", func(r tableStatRow) bool { return r.Inserts == 2 })
			fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
			sys := xactSysCumulative(t, p, c, name)
			if sys.Inserts != 2 || sys.Updates != 0 || sys.Deletes != 0 || published.Updates != 0 || published.Deletes != 0 {
				t.Fatal("cumulative executed vector", sys, published)
			}
			count := int64(0)
			if end == "COMMIT" {
				count = 2
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM information_schema."+name, []string{"count"}, []string{"INT8"}, [][]any{{count}}))
			if old != held {
				t.Fatal("retained local result changed", old, held)
			}
			xactTableCounts(t, old, 2, 0, 0)
		}
		xactSysCreate(t, p, b, c, "xst_frames")
		w, retire := fixture.StatisticsReportingConn(t, p, b)
		defer retire()
		fixture.StatisticsExec(t, p, b, w, "BEGIN", 0)
		fixture.StatisticsExec(t, p, b, w, "INSERT INTO information_schema.xst_frames VALUES(1,NULL),(2,'b')", 2)
		old := xactSysGet(t, p, w, "xst_frames")
		xactTableCounts(t, old, 2, 0, 0)
		fixture.StatisticsExec(t, p, b, w, "SAVEPOINT child", 0)
		fixture.StatisticsExec(t, p, b, w, "INSERT INTO information_schema.xst_frames VALUES(3,NULL)", 1)
		xactTableCounts(t, xactSysGet(t, p, w, "xst_frames"), 3, 0, 0)
		fixture.StatisticsExec(t, p, b, w, "ROLLBACK TO child", 0)
		xactTableCounts(t, xactSysGet(t, p, w, "xst_frames"), 3, 0, 0)
		fixture.RunStep(t, p.Ctx, w, b.Recorder, fixture.Select("SELECT count(*) FROM information_schema.xst_frames", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
		fixture.StatisticsExec(t, p, b, w, "UPDATE information_schema.xst_frames SET payload='u' WHERE n=1", 1)
		fixture.StatisticsExec(t, p, b, w, "DELETE FROM information_schema.xst_frames WHERE n=1", 1)
		updated := xactSysGet(t, p, w, "xst_frames")
		xactTableCounts(t, updated, 3, 1, 1)
		if !b.PG && (updated.HOT != 0 || updated.Newpage != 0) {
			t.Fatal("native physical estimates", updated)
		}
		xactTableCounts(t, old, 2, 0, 0)
		xactTableEnd(t, p, b, w, "ROLLBACK")
	})
}

func TestReferencePGStatXactSysTablesCurrentState(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, mode := range []string{"none", "cache", "snapshot"} {
			name := "xst_" + mode
			xactSysCreate(t, p, b, c, name)
			w, retire := fixture.StatisticsReportingConn(t, p, b)
			fixture.StatisticsExec(t, p, b, w, "BEGIN", 0)
			fixture.StatisticsExec(t, p, b, w, "SET LOCAL stats_fetch_consistency="+mode, 0)
			stmt, err := w.PrepareContext(p.Ctx, xactSysSelect)
			if err != nil {
				t.Fatal(err)
			}
			get := func() xactTableRow {
				var r xactTableRow
				if err := stmt.QueryRowContext(p.Ctx, name).Scan(r.destinations()...); err != nil {
					t.Fatal(err)
				}
				return r
			}
			fixture.StatisticsExec(t, p, b, w, "INSERT INTO information_schema."+name+" VALUES(1,NULL)", 1)
			old := get()
			xactTableCounts(t, old, 1, 0, 0)
			if cut := tableStatGet(t, p, w, name); cut.Inserts != 0 {
				t.Fatal("unpublished work reached cumulative cut", cut)
			}
			fixture.StatisticsExec(t, p, b, w, "INSERT INTO information_schema."+name+" VALUES(2,NULL)", 1)
			xactTableCounts(t, get(), 2, 0, 0)
			fixture.StatisticsClearSnapshot(t, p, b, w)
			xactTableCounts(t, get(), 2, 0, 0)
			if cut := tableStatGet(t, p, w, name); cut.Inserts != 0 {
				t.Fatal("clear published work", cut)
			}
			fixture.StatisticsExec(t, p, b, w, "SET LOCAL track_counts=off", 0)
			fixture.StatisticsExec(t, p, b, w, "INSERT INTO information_schema."+name+" VALUES(3,NULL)", 1)
			xactTableCounts(t, get(), 2, 0, 0)
			fixture.StatisticsExec(t, p, b, w, "SET LOCAL track_counts=on", 0)
			fixture.StatisticsExec(t, p, b, w, "INSERT INTO information_schema."+name+" VALUES(4,NULL)", 1)
			xactTableCounts(t, get(), 3, 0, 0)
			xactTableCounts(t, old, 1, 0, 0)
			stmt.Close()
			xactTableEnd(t, p, b, w, "ROLLBACK")
			retire()
		}
	})
}

func TestReferencePGStatXactSysTablesAccess(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xactSysCreate(t, p, b, c, "xst_seq")
		xactSysSeed(t, p, b, "xst_seq")
		xactSysCreate(t, p, b, c, "xst_empty")
		w, retire := fixture.StatisticsReportingConn(t, p, b)
		defer retire()
		tableStatSeqPrefer(t, p, b, w)
		fixture.StatisticsExec(t, p, b, w, "BEGIN", 0)
		stmt, err := w.PrepareContext(p.Ctx, "SELECT payload FROM information_schema.xst_seq WHERE n=$1")
		if err != nil {
			t.Fatal(err)
		}
		stmt.Close()
		if r := xactSysGet(t, p, w, "xst_seq"); r.Seq != 0 || r.Read != 0 {
			t.Fatal("prepare executed heap", r)
		}
		var scans, read int64
		for i, tc := range []struct {
			q           string
			rows        [][]any
			scans, read int64
		}{
			{"SELECT payload FROM information_schema.xst_seq WHERE n=2", [][]any{{nil}}, 1, 3},
			{"SELECT payload FROM information_schema.xst_seq WHERE n=99", nil, 1, 3},
			{"SELECT payload FROM information_schema.xst_seq WHERE FALSE", nil, 0, 0},
			{"SELECT payload FROM information_schema.xst_seq LIMIT 0", nil, 0, 0},
		} {
			if i < 2 {
				tableStatSeqPlan(t, b, indexStatPlan(t, p, w, tc.q))
			}
			step := fixture.Select(tc.q, []string{"payload"}, []string{"TEXT"}, tc.rows)
			step.Prepared = i%2 == 1
			fixture.RunStep(t, p.Ctx, w, b.Recorder, step)
			scans += tc.scans
			read += tc.read
			r := xactSysGet(t, p, w, "xst_seq")
			if r.Seq != scans || r.Read != read || r.Index.Valid || r.Fetch.Valid {
				t.Fatal("actual reached SYS heap", r, scans, read)
			}
		}
		fixture.RunStep(t, p.Ctx, w, b.Recorder, fixture.Select("SELECT payload FROM information_schema.xst_empty", []string{"payload"}, []string{"TEXT"}, nil))
		if r := xactSysGet(t, p, w, "xst_empty"); r.Seq != 1 || r.Read != 0 {
			t.Fatal("reached empty SYS heap", r)
		}
		xactTableEnd(t, p, b, w, "ROLLBACK")
		xactSysCreate(t, p, b, c, "xst_indexed")
		xactSysSeed(t, p, b, "xst_indexed")
		tableStatBuild(t, p, b, "xst_indexed", 1, 3, "CREATE INDEX xst_hash ON information_schema.xst_indexed USING hash(n)")
		xactTableIndex(t, xactSysGet(t, p, c, "xst_indexed"), 0, 0)
		indexed, retireIndexed := fixture.StatisticsReportingConn(t, p, b)
		defer retireIndexed()
		indexStatPrefer(t, p, b, indexed)
		q := "SELECT payload FROM information_schema.xst_indexed WHERE n=2"
		batch, err := indexed.QueryContext(p.Ctx, "EXPLAIN (COSTS OFF) "+q+";"+q+";SELECT * FROM pg_stat_xact_sys_tables WHERE relname='xst_indexed'")
		if err != nil {
			t.Fatal(err)
		}
		defer batch.Close()
		method := xactTableBatchPlan(t, batch)
		if !indexStatSimple(t, b, method) {
			t.Fatal("actual SYS index path", method)
		}
		if !batch.NextResultSet() {
			t.Fatal("missing indexed output", batch.Err())
		}
		fixture.BatchReadSet(t, batch, fixture.BatchSet{Columns: []string{"payload"}, Types: []string{"TEXT"}, Rows: [][]any{{nil}}})
		if !batch.NextResultSet() {
			t.Fatal("missing SYS pending result", batch.Err())
		}
		r := xactTableBatchRow(t, batch, b.PG)
		xactTableIndex(t, r, 1, 1)
		if r.Seq != 0 || r.Read != 0 {
			t.Fatal("index fabricated heap work", r)
		}
		if batch.NextResultSet() || batch.Err() != nil {
			t.Fatal("unexpected batch tail", batch.Err())
		}
	})
}
