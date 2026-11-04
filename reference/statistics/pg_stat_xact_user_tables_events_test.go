package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatXactUserTablesTransactions(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, end := range []string{"ROLLBACK", "COMMIT"} {
			name := "xut_abort"
			if end == "COMMIT" {
				name = "xut_commit"
			}
			xactUserCreate(t, p, b, c, name)
			w, retire := fixture.StatisticsReportingConn(t, p, b)
			fixture.StatisticsExec(t, p, b, w, "BEGIN", 0)
			stmt, err := w.PrepareContext(p.Ctx, xactUserSelect)
			if err != nil {
				t.Fatal(err)
			}
			xactTableCounts(t, xactUserGet(t, p, w, name), 0, 0, 0)
			fixture.StatisticsExec(t, p, b, w, "INSERT INTO public."+name+" VALUES(1,NULL),(2,'b')", 2)
			old := xactUserGet(t, p, w, name)
			xactTableCounts(t, old, 2, 0, 0)
			var held xactTableRow
			if err := stmt.QueryRowContext(p.Ctx, name).Scan(held.destinations()...); err != nil {
				t.Fatal(err)
			}
			stmt.Close()
			xactTableCounts(t, held, 2, 0, 0)
			xactTableCounts(t, xactTableGet(t, p, w, name), 2, 0, 0)
			xactTableCounts(t, xactUserGet(t, p, c, name), 0, 0, 0)
			xactTableEnd(t, p, b, w, end)
			fixture.StatisticsExec(t, p, b, w, "BEGIN", 0)
			xactTableCounts(t, xactUserGet(t, p, w, name), 0, 0, 0)
			fixture.StatisticsExec(t, p, b, w, "ROLLBACK", 0)
			retire()
			published := tableStatAwait(t, p, b, name, "executed USER inserts2", func(r tableStatRow) bool { return r.Inserts == 2 })
			fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
			sys := xactUserCumulative(t, p, c, name)
			if sys.Inserts != 2 || sys.Updates != 0 || sys.Deletes != 0 || published.Updates != 0 || published.Deletes != 0 {
				t.Fatal("cumulative executed vector", sys, published)
			}
			count := int64(0)
			if end == "COMMIT" {
				count = 2
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM public."+name, []string{"count"}, []string{"INT8"}, [][]any{{count}}))
			if old != held {
				t.Fatal("retained local result changed", old, held)
			}
			xactTableCounts(t, old, 2, 0, 0)
		}
		xactUserCreate(t, p, b, c, "xut_frames")
		w, retire := fixture.StatisticsReportingConn(t, p, b)
		defer retire()
		fixture.StatisticsExec(t, p, b, w, "BEGIN", 0)
		fixture.StatisticsExec(t, p, b, w, "INSERT INTO public.xut_frames VALUES(1,NULL),(2,'b')", 2)
		old := xactUserGet(t, p, w, "xut_frames")
		xactTableCounts(t, old, 2, 0, 0)
		fixture.StatisticsExec(t, p, b, w, "SAVEPOINT child", 0)
		fixture.StatisticsExec(t, p, b, w, "INSERT INTO public.xut_frames VALUES(3,NULL)", 1)
		xactTableCounts(t, xactUserGet(t, p, w, "xut_frames"), 3, 0, 0)
		fixture.StatisticsExec(t, p, b, w, "ROLLBACK TO child", 0)
		xactTableCounts(t, xactUserGet(t, p, w, "xut_frames"), 3, 0, 0)
		fixture.RunStep(t, p.Ctx, w, b.Recorder, fixture.Select("SELECT count(*) FROM public.xut_frames", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
		fixture.StatisticsExec(t, p, b, w, "UPDATE public.xut_frames SET payload='u' WHERE n=1", 1)
		fixture.StatisticsExec(t, p, b, w, "DELETE FROM public.xut_frames WHERE n=1", 1)
		updated := xactUserGet(t, p, w, "xut_frames")
		xactTableCounts(t, updated, 3, 1, 1)
		if !b.PG && (updated.HOT != 0 || updated.Newpage != 0) {
			t.Fatal("native physical estimates", updated)
		}
		xactTableCounts(t, old, 2, 0, 0)
		xactTableEnd(t, p, b, w, "ROLLBACK")
		fixture.StatisticsExec(t, p, b, c, "CREATE TABLE public.xut_atomic(n integer NOT NULL)", 0)
		fixture.StatisticsExec(t, p, b, w, "BEGIN", 0)
		fixture.StatisticsExec(t, p, b, w, "SAVEPOINT failed", 0)
		// Ordinary native VALUES validates the whole input before staging;
		// PostgreSQL has really inserted one prefix tuple before the failure.
		plain := fixture.ImplicitError("INSERT INTO public.xut_atomic VALUES(1),(NULL)", fixture.Diagnostic{Code: "23502", Message: `null value in column "n" of relation "xut_atomic" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "xut_atomic", Column: "n"})
		plain.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, w, b.Recorder, plain)
		fixture.StatisticsExec(t, p, b, w, "ROLLBACK TO failed", 0)
		inserted := int64(0)
		if b.PG {
			inserted = 1
		}
		xactTableCounts(t, xactUserGet(t, p, w, "xut_atomic"), inserted, 0, 0)
		fixture.RunStep(t, p.Ctx, w, b.Recorder, fixture.Select("SELECT count(*) FROM public.xut_atomic", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		// RETURNING reaches the incremental accepted-row seam on both
		// backends. Only the executed prefix counts; no data survives abort.
		fixture.StatisticsExec(t, p, b, w, "SAVEPOINT streamed", 0)
		streamed := fixture.ImplicitError("INSERT INTO public.xut_atomic VALUES(2),(NULL) RETURNING n", fixture.Diagnostic{Code: "23502", Message: `null value in column "n" of relation "xut_atomic" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "xut_atomic", Column: "n"})
		streamed.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, w, b.Recorder, streamed)
		fixture.StatisticsExec(t, p, b, w, "ROLLBACK TO streamed", 0)
		xactTableCounts(t, xactUserGet(t, p, w, "xut_atomic"), inserted+1, 0, 0)
		fixture.RunStep(t, p.Ctx, w, b.Recorder, fixture.Select("SELECT count(*) FROM public.xut_atomic", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		xactTableEnd(t, p, b, w, "ROLLBACK")
	})
}

func TestReferencePGStatXactUserTablesCurrentState(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, mode := range []string{"none", "cache", "snapshot"} {
			name := "xut_" + mode
			xactUserCreate(t, p, b, c, name)
			w, retire := fixture.StatisticsReportingConn(t, p, b)
			fixture.StatisticsExec(t, p, b, w, "BEGIN", 0)
			fixture.StatisticsExec(t, p, b, w, "SET LOCAL stats_fetch_consistency="+mode, 0)
			stmt, err := w.PrepareContext(p.Ctx, xactUserSelect)
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
			fixture.StatisticsExec(t, p, b, w, "INSERT INTO public."+name+" VALUES(1,NULL)", 1)
			old := get()
			xactTableCounts(t, xactUserGet(t, p, c, name), 0, 0, 0)
			xactTableCounts(t, old, 1, 0, 0)
			if cut := tableStatGet(t, p, w, name); cut.Inserts != 0 {
				t.Fatal("unpublished work reached cumulative cut", cut)
			}
			fixture.StatisticsExec(t, p, b, w, "INSERT INTO public."+name+" VALUES(2,NULL)", 1)
			xactTableCounts(t, get(), 2, 0, 0)
			fixture.StatisticsClearSnapshot(t, p, b, w)
			xactTableCounts(t, get(), 2, 0, 0)
			if cut := tableStatGet(t, p, w, name); cut.Inserts != 0 {
				t.Fatal("clear published work", cut)
			}
			fixture.StatisticsExec(t, p, b, w, "SET LOCAL track_counts=off", 0)
			fixture.StatisticsExec(t, p, b, w, "INSERT INTO public."+name+" VALUES(3,NULL)", 1)
			xactTableCounts(t, get(), 2, 0, 0)
			fixture.StatisticsExec(t, p, b, w, "SET LOCAL track_counts=on", 0)
			fixture.StatisticsExec(t, p, b, w, "INSERT INTO public."+name+" VALUES(4,NULL)", 1)
			xactTableCounts(t, get(), 3, 0, 0)
			xactTableCounts(t, xactUserGet(t, p, c, name), 0, 0, 0)
			xactTableCounts(t, old, 1, 0, 0)
			stmt.Close()
			xactTableEnd(t, p, b, w, "ROLLBACK")
			retire()
		}
	})
}

