package reference_test

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferencePGStatAllTablesSequentialAccess(t *testing.T) {
	tableStatSequentialAccess(t, tableStatFamily{})
}

func tableStatSequentialAccess(t *testing.T, f tableStatFamily) {
	f.scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
		f.create(t, p, b, c, "sat_seq")
		f.seed(t, p, b, "sat_seq")
		f.create(t, p, b, c, "sat_seq_empty")
		f.create(t, p, b, c, "sat_seq_marker")
		if initial := f.get(t, p, c, "sat_seq"); initial.Seq != 0 || initial.Read != 0 || initial.LastSeq.Valid {
			t.Fatal("unreached source had scans", initial)
		}
		preparer, retirePrepare := f.reporting(t, p, b)
		prepared, err := preparer.PrepareContext(p.Ctx, "SELECT payload FROM sat_seq WHERE n=$1")
		if err != nil {
			t.Fatal(err)
		}
		prepared.Close()
		retirePrepare()
		if r := f.get(t, p, c, "sat_seq"); r.Seq != 0 || r.Read != 0 {
			t.Fatal("prepare executed source", r)
		}
		var scans, read int64
		for i, tc := range []struct {
			name, q, column, typ string
			rows                 [][]any
			scans, read          int64
		}{
			{"filter", "SELECT payload FROM sat_seq WHERE n=2", "payload", "TEXT", [][]any{{nil}}, 1, 3},
			{"miss", "SELECT payload FROM sat_seq WHERE n=99", "payload", "TEXT", nil, 1, 3},
			{"limit0", "SELECT payload FROM sat_seq LIMIT 0", "payload", "TEXT", nil, 0, 0},
			{"false", "SELECT payload FROM sat_seq WHERE FALSE", "payload", "TEXT", nil, 0, 0},
			{"limit1", "SELECT count(*) FROM (SELECT payload FROM sat_seq LIMIT 1) s", "count", "INT8", [][]any{{int64(1)}}, 1, 1},
			{"exists", "SELECT EXISTS(SELECT n FROM sat_seq)", "exists", "BOOL", [][]any{{true}}, 1, 1},
			{"blocking", "SELECT payload FROM sat_seq ORDER BY n DESC LIMIT 1", "payload", "TEXT", [][]any{{"c"}}, 1, 3},
			{"cte_replay", "WITH x AS MATERIALIZED(SELECT n FROM sat_seq) SELECT count(*) FROM x a CROSS JOIN x b", "count", "INT8", [][]any{{int64(9)}}, 1, 3},
		} {
			w, retire := f.reporting(t, p, b)
			tableStatSeqPrefer(t, p, b, w)
			if tc.name == "filter" || tc.name == "miss" {
				tableStatSeqPlan(t, b, indexStatPlan(t, p, w, tc.q))
			}
			step := fixture.Select(tc.q, []string{tc.column}, []string{tc.typ}, tc.rows)
			step.Prepared = i%2 == 1
			fixture.RunStep(t, p.Ctx, w, b.Recorder, step)
			fixture.StatisticsExec(t, p, b, w, fmt.Sprintf("INSERT INTO sat_seq_marker VALUES(%d,NULL)", i), 1)
			retire()
			f.await(t, p, b, "sat_seq_marker", "retired sequential writer", func(r tableStatRow) bool { return r.Inserts == int64(i+1) })
			scans += tc.scans
			read += tc.read
			r := f.await(t, p, b, "sat_seq", tc.name, func(r tableStatRow) bool { return r.Seq == scans && r.Read == read })
			if !r.LastSeq.Valid || r.Inserts != 3 || r.Index.Valid || r.Fetch.Valid {
				t.Fatal("sequential source accounting", tc.name, r)
			}
			var raw any
			if err := c.QueryRowContext(p.Ctx, "SELECT last_seq_scan FROM pg_catalog."+f.view()+" WHERE relname='sat_seq'").Scan(&raw); err != nil {
				t.Fatal(err)
			}
			if _, ok := raw.(time.Time); !ok {
				t.Fatalf("finite TIMESTAMPTZ carrier=%T", raw)
			}
		}
		w, retire := f.reporting(t, p, b)
		tableStatSeqPrefer(t, p, b, w)
		fixture.RunStep(t, p.Ctx, w, b.Recorder, fixture.Select("SELECT payload FROM sat_seq_empty", []string{"payload"}, []string{"TEXT"}, nil))
		retire()
		empty := f.await(t, p, b, "sat_seq_empty", "reached empty heap", func(r tableStatRow) bool { return r.Seq == 1 })
		if empty.Read != 0 || !empty.LastSeq.Valid {
			t.Fatal("empty heap scan", empty)
		}
		for _, end := range []string{"COMMIT", "ROLLBACK"} {
			w, retire := f.reporting(t, p, b)
			tableStatSeqPrefer(t, p, b, w)
			fixture.StatisticsExec(t, p, b, w, "BEGIN", 0)
			fixture.RunStep(t, p.Ctx, w, b.Recorder, fixture.Select("SELECT payload FROM sat_seq WHERE n=2", []string{"payload"}, []string{"TEXT"}, [][]any{{nil}}))
			lower := indexStatClock(t, p, w)
			if r := f.get(t, p, c, "sat_seq"); r.Seq != scans {
				t.Fatal("read published inside transaction", r)
			}
			fixture.StatisticsExec(t, p, b, w, end, 0)
			retire()
			upper := indexStatClock(t, p, c)
			scans++
			read += 3
			r := f.await(t, p, b, "sat_seq", "read survives "+end, func(r tableStatRow) bool { return r.Seq == scans && r.Read == read })
			if !r.LastSeq.Valid || r.LastSeq.Time.Before(lower) || r.LastSeq.Time.After(upper) {
				t.Fatal("scan time not transaction stop", lower, upper, r)
			}
		}
	})
}

func TestReferencePGStatAllTablesIndexAggregation(t *testing.T) {
	tableStatIndexAggregation(t, tableStatFamily{})
}

func tableStatIndexAggregation(t *testing.T, f tableStatFamily) {
	f.scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
		f.create(t, p, b, c, "sat_indexes")
		f.seed(t, p, b, "sat_indexes")
		if r := f.get(t, p, c, "sat_indexes"); r.Index.Valid || r.Fetch.Valid || r.LastIndex.Valid {
			t.Fatal("no-index SUM/MAX not NULL", r)
		}
		baseline := f.build(t, p, b, "sat_indexes", 3, 9, "CREATE INDEX sat_hash_n ON sat_indexes USING hash(n)", "CREATE INDEX sat_hash_payload ON sat_indexes USING hash(payload)", "CREATE INDEX sat_tree_untouched ON sat_indexes(n) WHERE n<0")
		tableStatIndexWant(t, f.get(t, p, c, "sat_indexes"), 0, 0)
		for i, tc := range []struct {
			q, index string
			rows     [][]any
		}{{"SELECT payload FROM sat_indexes WHERE n=2", "sat_hash_n", [][]any{{nil}}}, {"SELECT payload FROM sat_indexes WHERE payload='a'", "sat_hash_payload", [][]any{{"a"}}}, {"SELECT payload FROM sat_indexes WHERE n=99", "sat_hash_n", nil}} {
			w, retire := f.reporting(t, p, b)
			indexStatPrefer(t, p, b, w)
			method := indexStatPlan(t, p, w, tc.q)
			if !indexStatSimple(t, b, method) || !strings.Contains(method, tc.index) {
				t.Fatal("independent attached-index method", method, tc.index)
			}
			fixture.RunStep(t, p.Ctx, w, b.Recorder, fixture.Select(tc.q, []string{"payload"}, []string{"TEXT"}, tc.rows))
			retire()
			r := f.await(t, p, b, "sat_indexes", "attached SUM", func(r tableStatRow) bool { return r.Index.Valid && r.Index.Int64 == int64(i+1) })
			fetch := int64(i + 1)
			if i == 2 {
				fetch = 2
			}
			tableStatIndexWant(t, r, int64(i+1), fetch)
			if r.Seq != baseline.Seq || r.Read != baseline.Read || !r.LastSeq.Time.Equal(baseline.LastSeq.Time) {
				t.Fatal("index path fabricated sequential work", r, baseline)
			}
		}
		a := indexStatGet(t, p, c, "sat_hash_n")
		second := indexStatGet(t, p, c, "sat_hash_payload")
		indexStatWant(t, a, 2, 1, 1)
		indexStatWant(t, second, 1, 1, 1)
		indexStatWant(t, indexStatGet(t, p, c, "sat_tree_untouched"), 0, 0, 0)
		r := f.get(t, p, c, "sat_indexes")
		latest := a.Last.Time
		if second.Last.Time.After(latest) {
			latest = second.Last.Time
		}
		if !r.LastIndex.Valid || !r.LastIndex.Time.Equal(latest) {
			t.Fatal("attached timestamp MAX", r, a, second)
		}
		tableStatBitmap(t, p, b, c, f)
		tableStatIndexOnly(t, p, b, c, f)
	})
}

func tableStatBitmap(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, f tableStatFamily) {
	t.Helper()
	f.create(t, p, b, c, "sat_bitmap")
	for _, q := range []string{"CREATE EXTENSION bloom WITH SCHEMA public", "ALTER TABLE sat_bitmap SET(autovacuum_enabled=false)", "INSERT INTO sat_bitmap VALUES(2,'a'),(2,'b'),(3,'c'),(NULL,NULL)"} {
		count := int64(0)
		if strings.HasPrefix(q, "INSERT") {
			count = 4
		}
		fixture.StatisticsExec(t, p, b, c, q, count)
	}
	baseline := f.build(t, p, b, "sat_bitmap", 1, 4, "CREATE INDEX sat_bitmap_idx ON sat_bitmap USING bloom(n) WITH(length=1,col1=1)")
	w, retire := f.reporting(t, p, b)
	fixture.StatisticsExec(t, p, b, w, "SET enable_seqscan=off", 0)
	q := "SELECT payload FROM sat_bitmap WHERE n=2"
	method := indexStatPlan(t, p, w, q)
	if b.PG {
		if !strings.Contains(method, "Bitmap Heap Scan") || !strings.Contains(method, "Bitmap Index Scan on sat_bitmap_idx") {
			t.Fatal("real bitmap method", method)
		}
	} else if !strings.Contains(method, "Access Method: Bloom Candidates") {
		t.Fatal("real native bloom method", method)
	}
	// PostgreSQL reports recheck removals; native exposes the actual physical
	// source candidate count. Neither expectation reads the new provider.
	rows, err := w.QueryContext(p.Ctx, "EXPLAIN (ANALYZE,COSTS OFF,TIMING OFF,SUMMARY OFF) "+q)
	if err != nil {
		t.Fatal(err)
	}
	candidates := int64(2)
	if !b.PG {
		candidates = -1
	}
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		trim := strings.TrimSpace(line)
		if b.PG && strings.HasPrefix(trim, "Rows Removed by Index Recheck:") {
			n, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(trim, "Rows Removed by Index Recheck:")), 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			candidates += n
		}
		if !b.PG && strings.HasPrefix(trim, "Logical Scan on ") && strings.Contains(trim, "sat_bitmap") {
			_, after, ok := strings.Cut(trim, "(actual rows=")
			var actual float64
			var loops int
			if !ok {
				t.Fatal("unobserved native candidate source", line)
			}
			if _, err := fmt.Sscanf(after, "%f loops=%d)", &actual, &loops); err != nil || loops != 1 || actual != float64(int64(actual)) {
				t.Fatal("native candidate observation", line, err)
			}
			candidates = int64(actual)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if candidates < 2 {
		t.Fatal("missing independently observed live candidates", candidates)
	}
	fixture.StatisticsExec(t, p, b, w, "SET track_counts=off", 0)
	step := fixture.Select(q, []string{"payload"}, []string{"TEXT"}, [][]any{{"a"}, {"b"}})
	step.Bag = true
	fixture.RunStep(t, p.Ctx, w, b.Recorder, step)
	retire()
	r := f.await(t, p, b, "sat_bitmap", "bitmap heap publication", func(r tableStatRow) bool { return r.Index.Valid && r.Index.Int64 == 1 })
	tableStatIndexWant(t, r, 1, candidates)
	if r.Seq != baseline.Seq || r.Read != baseline.Read {
		t.Fatal("bitmap charged sequential scan", r, baseline)
	}
	index := indexStatGet(t, p, c, "sat_bitmap_idx")
	indexStatWant(t, index, 1, candidates, 0)
}

func tableStatIndexOnly(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, f tableStatFamily) {
	t.Helper()
	f.create(t, p, b, c, "sat_only")
	f.seed(t, p, b, "sat_only")
	baseline := f.build(t, p, b, "sat_only", 1, 3, "CREATE INDEX sat_only_tree ON sat_only(n)")
	fixture.StatisticsExec(t, p, b, c, "VACUUM sat_only", 0)
	w, retire := f.reporting(t, p, b)
	for _, q := range []string{"SET enable_seqscan=off", "SET enable_bitmapscan=off", "SET enable_indexonlyscan=on"} {
		fixture.StatisticsExec(t, p, b, w, q, 0)
	}
	if !b.PG {
		method := indexStatPlan(t, p, w, "SELECT n FROM sat_only WHERE n=2")
		tableStatSeqPlan(t, b, method)
		fixture.RunStep(t, p.Ctx, w, b.Recorder, fixture.Select("SELECT n FROM sat_only WHERE n=2", []string{"n"}, []string{"INT4"}, [][]any{{int64(2)}}))
		retire()
		r := f.await(t, p, b, "sat_only", "truthful unsupported B-tree fallback", func(r tableStatRow) bool { return r.Seq == baseline.Seq+1 })
		if r.Read != baseline.Read+3 {
			t.Fatal("fallback reads", r)
		}
		tableStatIndexWant(t, r, 0, 0)
		return
	}
	rows, err := w.QueryContext(p.Ctx, "EXPLAIN (ANALYZE,COSTS OFF,TIMING OFF,SUMMARY OFF) SELECT n FROM sat_only WHERE n=2")
	if err != nil {
		t.Fatal(err)
	}
	heap := int64(-1)
	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, line)
		if _, after, ok := strings.Cut(strings.TrimSpace(line), "Heap Fetches: "); ok {
			heap, err = strconv.ParseInt(after, 10, 64)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if !strings.Contains(strings.Join(lines, "\n"), "Index Only Scan using sat_only_tree") || heap < 0 {
		t.Fatal("actual index-only ownership", lines)
	}
	retire()
	r := f.await(t, p, b, "sat_only", "index-only publication", func(r tableStatRow) bool { return r.Index.Valid && r.Index.Int64 == 1 })
	tableStatIndexWant(t, r, 1, heap)
	if r.Seq != baseline.Seq || r.Read != baseline.Read {
		t.Fatal("index-only counted heap sequential work", r, baseline)
	}
}
