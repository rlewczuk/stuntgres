package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatXactUserTablesAccess(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xactUserCreate(t, p, b, c, "xut_seq")
		xactUserSeed(t, p, b, "xut_seq")
		xactUserCreate(t, p, b, c, "xut_empty")
		w, retire := fixture.StatisticsReportingConn(t, p, b)
		defer retire()
		tableStatSeqPrefer(t, p, b, w)
		fixture.StatisticsExec(t, p, b, w, "BEGIN", 0)
		stmt, err := w.PrepareContext(p.Ctx, "SELECT payload FROM public.xut_seq WHERE n=$1")
		if err != nil {
			t.Fatal(err)
		}
		stmt.Close()
		if r := xactUserGet(t, p, w, "xut_seq"); r.Seq != 0 || r.Read != 0 {
			t.Fatal("prepare executed heap", r)
		}
		var scans, read int64
		for i, tc := range []struct {
			q           string
			rows        [][]any
			scans, read int64
		}{
			{"SELECT payload FROM public.xut_seq WHERE n=2", [][]any{{nil}}, 1, 3},
			{"SELECT payload FROM public.xut_seq WHERE n=99", nil, 1, 3},
			{"SELECT payload FROM public.xut_seq WHERE FALSE", nil, 0, 0},
			{"SELECT payload FROM public.xut_seq LIMIT 0", nil, 0, 0},
		} {
			if i < 2 {
				tableStatSeqPlan(t, b, indexStatPlan(t, p, w, tc.q))
			}
			step := fixture.Select(tc.q, []string{"payload"}, []string{"TEXT"}, tc.rows)
			step.Prepared = i%2 == 1
			fixture.RunStep(t, p.Ctx, w, b.Recorder, step)
			scans += tc.scans
			read += tc.read
			r := xactUserGet(t, p, w, "xut_seq")
			if r.Seq != scans || r.Read != read || r.Index.Valid || r.Fetch.Valid {
				t.Fatal("actual reached USER heap", r, scans, read)
			}
		}
		fixture.RunStep(t, p.Ctx, w, b.Recorder, fixture.Select("SELECT payload FROM public.xut_empty", []string{"payload"}, []string{"TEXT"}, nil))
		if r := xactUserGet(t, p, w, "xut_empty"); r.Seq != 1 || r.Read != 0 {
			t.Fatal("reached empty USER heap", r)
		}
		xactTableEnd(t, p, b, w, "ROLLBACK")
		xactUserCreate(t, p, b, c, "xut_indexed")
		xactUserSeed(t, p, b, "xut_indexed")
		tableStatBuild(t, p, b, "xut_indexed", 2, 6, "CREATE INDEX xut_hash ON public.xut_indexed USING hash(n)", "CREATE INDEX xut_payload_hash ON public.xut_indexed USING hash(payload)")
		xactTableIndex(t, xactUserGet(t, p, c, "xut_indexed"), 0, 0)
		indexed, retireIndexed := fixture.StatisticsReportingConn(t, p, b)
		defer retireIndexed()
		indexStatPrefer(t, p, b, indexed)
		// Keep every EXPLAIN, demanded access and pending projection in one
		// implicit message. Explicit BEGIN makes native published hash roots
		// ineligible; separate messages can publish between counter captures.
		cases := []struct {
			q, index       string
			rows           [][]any
			scans, fetches int64
		}{
			{"SELECT payload FROM public.xut_indexed WHERE n=2", "xut_hash", [][]any{{nil}}, 1, 1},
			{"SELECT payload FROM public.xut_indexed WHERE n=99", "xut_hash", nil, 2, 1},
			{"SELECT payload FROM public.xut_indexed WHERE payload='a'", "xut_payload_hash", [][]any{{"a"}}, 3, 2},
			{"SELECT payload FROM public.xut_indexed WHERE payload='missing'", "xut_payload_hash", nil, 4, 2},
		}
		var commands []string
		for _, tc := range cases {
			commands = append(commands, "EXPLAIN (COSTS OFF) "+tc.q, tc.q, "SELECT * FROM pg_stat_xact_user_tables WHERE relname='xut_indexed'")
		}
		batch, err := indexed.QueryContext(p.Ctx, strings.Join(commands, ";"))
		if err != nil {
			t.Fatal(err)
		}
		defer batch.Close()
		for i, tc := range cases {
			if i > 0 && !batch.NextResultSet() {
				t.Fatal("missing indexed plan", i, batch.Err())
			}
			method := xactTableBatchPlan(t, batch)
			if !indexStatSimple(t, b, method) || !strings.Contains(method, tc.index) {
				t.Fatal("actual USER index path", i, method)
			}
			if !batch.NextResultSet() {
				t.Fatal("missing indexed output", batch.Err())
			}
			fixture.BatchReadSet(t, batch, fixture.BatchSet{Columns: []string{"payload"}, Types: []string{"TEXT"}, Rows: tc.rows})
			if !batch.NextResultSet() {
				t.Fatal("missing USER pending result", batch.Err())
			}
			r := xactTableBatchRow(t, batch, b.PG)
			xactTableIndex(t, r, tc.scans, tc.fetches)
			if r.Seq != 0 || r.Read != 0 {
				t.Fatal("index fabricated heap work", r)
			}
		}
		if batch.NextResultSet() || batch.Err() != nil {
			t.Fatal("unexpected batch tail", batch.Err())
		}
		batch.Close()
	})
}
