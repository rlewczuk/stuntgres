package reference_test

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceAnalyzeDistributions(t *testing.T) {
	analyzeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { analyzeRun(t, p, b, b.DB, s) }
		run(fixture.Exec("CREATE TABLE analyze_skew(a INT,b INT)", 0))
		analyzeTuples(t, p, b, b.DB, "analyze_skew", -1)
		analyzeInsert(t, p, b, b.DB, "analyze_skew", 14, 2, 4)
		started := time.Now().Add(-time.Second)
		run(fixture.Exec("ANALYZE analyze_skew", 0))
		analyzeTuples(t, p, b, b.DB, "analyze_skew", 20)
		for _, tc := range []struct {
			predicate string
			n         int64
		}{{"a=1", 14}, {"a=2", 2}, {"a IS NULL", 4}, {"a IS NOT NULL", 16}, {"1=a", 14}, {"a<>1", 2}} {
			analyzeWantEstimate(t, p, b.DB, "analyze_skew", tc.predicate, tc.n)
		}
		for _, tc := range []struct {
			predicate string
			value     any
			n         int
		}{{"a=1", int64(1), 14}, {"a=2", int64(2), 2}, {"a IS NULL", nil, 4}} {
			run(fixture.Select("SELECT a FROM analyze_skew WHERE "+tc.predicate+" ORDER BY a", []string{"a"}, []string{"INT4"}, analyzeCountRows(tc.n, tc.value)))
		}
		// Follow the existing cumulative-statistics convention: independently
		// read fresh autocommit snapshots until PostgreSQL flushes its counter.
		deadline, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
		defer cancel()
		for {
			var count int64
			err := b.DB.QueryRowContext(deadline, "SELECT analyze_count FROM pg_stat_all_tables WHERE relname='analyze_skew'").Scan(&count)
			if err != nil {
				t.Fatal(err)
			}
			if count == 1 {
				break
			}
			if deadline.Err() != nil {
				t.Fatalf("analyze_count=%d want1", count)
			}
			runtime.Gosched()
		}
		run(fixture.Select("SELECT analyze_count,last_analyze IS NOT NULL AS recorded,vacuum_count,last_vacuum IS NULL AS untouched FROM pg_stat_all_tables WHERE relname='analyze_skew'", []string{"analyze_count", "recorded", "vacuum_count", "untouched"}, []string{"INT8", "BOOL", "INT8", "BOOL"}, [][]any{{int64(1), true, int64(0), true}}))
		run(fixture.Select("SELECT last_analyze >= $1::timestamptz AND last_analyze <= $2::timestamptz AS truthful FROM pg_stat_all_tables WHERE relname='analyze_skew'", []string{"truthful"}, []string{"BOOL"}, [][]any{{true}}, started, time.Now().Add(time.Second)))
		run(fixture.Exec("CREATE TABLE analyze_uniform(a INT)", 0))
		var vals []string
		for i := range 10 {
			vals = append(vals, fmt.Sprintf("(%d)", i+1))
		}
		run(fixture.Exec("INSERT INTO analyze_uniform VALUES "+strings.Join(vals, ","), 10))
		run(fixture.Exec("ANALYZE analyze_uniform", 0))
		analyzeTuples(t, p, b, b.DB, "analyze_uniform", 10)
		analyzeWantEstimate(t, p, b.DB, "analyze_uniform", "a=5", 1)
		less, atMost, greater := int64(4), int64(5), int64(5)
		if b.PG {
			// Observed pinned PostgreSQL 18.6 histogram estimates differ from
			// native deterministic full-data ranks; estimates are not row counts.
			less, atMost, greater = 3, 4, 6
		}
		analyzeWantEstimate(t, p, b.DB, "analyze_uniform", "a<5", less)
		analyzeWantEstimate(t, p, b.DB, "analyze_uniform", "a<=5", atMost)
		analyzeWantEstimate(t, p, b.DB, "analyze_uniform", "a>5", greater)
		run(fixture.Exec("CREATE TABLE analyze_null(a INT)", 0))
		run(fixture.Exec("INSERT INTO analyze_null VALUES(NULL),(NULL),(NULL),(NULL),(NULL)", 5))
		run(fixture.Exec("ANALYZE analyze_null", 0))
		analyzeTuples(t, p, b, b.DB, "analyze_null", 5)
		analyzeWantEstimate(t, p, b.DB, "analyze_null", "a IS NULL", 5)
		zero := int64(0)
		if b.PG {
			zero = 1
		} // PostgreSQL clamps estimated output rows, native does not.
		analyzeWantEstimate(t, p, b.DB, "analyze_null", "a=1", zero)
	})
}

func TestReferenceAnalyzeRefreshColumns(t *testing.T) {
	analyzeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) { analyzeRefresh(t, p, b, false) })
}

func analyzeRefresh(t *testing.T, p *fixture.Pair, b fixture.Backend, smoke bool) {
	t.Helper()
	run := func(s fixture.Step) { analyzeRun(t, p, b, b.DB, s) }
	run(fixture.Exec("CREATE TABLE analyze_refresh(a INT,b INT)", 0))
	analyzeInsert(t, p, b, b.DB, "analyze_refresh", 14, 2, 4)
	run(fixture.Exec("ANALYZE analyze_refresh", 0))
	analyzeTuples(t, p, b, b.DB, "analyze_refresh", 20)
	analyzeWantEstimate(t, p, b.DB, "analyze_refresh", "a=2", 2)
	analyzeWantEstimate(t, p, b.DB, "analyze_refresh", "a IS NULL", 4)
	initial := append(analyzeCountRows(14, int64(1)), analyzeCountRows(2, int64(2))...)
	initial = append(initial, analyzeCountRows(4, nil)...)
	run(fixture.Select("SELECT a FROM analyze_refresh ORDER BY a NULLS LAST", []string{"a"}, []string{"INT4"}, initial))
	q := "EXPLAIN SELECT * FROM analyze_refresh WHERE a=1"
	h := analyzeHeld(t, p, b.DB, q)
	read := func() []string {
		rows, err := h.QueryContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		return explainTextRows(t, rows)
	}
	old := read()
	saved := strings.Join(old, "\n")
	if analyzePlanEstimate(t, old) != 14 {
		t.Fatal(old)
	}
	run(fixture.Exec("UPDATE analyze_refresh SET a=2,b=2 WHERE a=1", 14))
	run(fixture.Exec("UPDATE analyze_refresh SET a=1,b=1 WHERE a IS NULL", 4))
	analyzeWantEstimate(t, p, b.DB, "analyze_refresh", "a=1", 14)
	analyzeWantEstimate(t, p, b.DB, "analyze_refresh", "b=1", 14)
	run(fixture.Exec("ANALYZE analyze_refresh(a)", 0))
	if got := analyzePlanEstimate(t, read()); got != 4 {
		t.Fatalf("held refresh=%d want4", got)
	}
	analyzeWantEstimate(t, p, b.DB, "analyze_refresh", "b=1", 14)
	if strings.Join(old, "\n") != saved {
		t.Fatal("saved EXPLAIN changed")
	}
	run(fixture.Exec("ANALYZE analyze_refresh(b)", 0))
	analyzeWantEstimate(t, p, b.DB, "analyze_refresh", "b=1", 4)
	want := append(analyzeCountRows(4, int64(1)), analyzeCountRows(16, int64(2))...)
	run(fixture.Select("SELECT a FROM analyze_refresh ORDER BY a", []string{"a"}, []string{"INT4"}, want))
	if smoke {
		var notices []fixture.Diagnostic
		_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec("ANALYZE VERBOSE analyze_refresh", 0))
		if len(notices) == 0 || notices[0].Code != "00000" || notices[0].Message != `analyzing "public.analyze_refresh"` {
			t.Fatal(notices)
		}
		run(fixture.ErrorDiagnostic("ANALYZE analyze_refresh(nope)", analyzeError("42703", `column "nope" of relation "analyze_refresh" does not exist`)))
		run(fixture.Exec("DELETE FROM analyze_refresh", 20))
		run(fixture.Exec("ANALYZE analyze_refresh", 0))
		analyzeTuples(t, p, b, b.DB, "analyze_refresh", 0)
	}
}

func TestReferenceAnalyzeSmoke(t *testing.T) {
	analyzeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) { analyzeRefresh(t, p, b, true) })
}

func TestReferenceAnalyzeResultInvariance(t *testing.T) {
	analyzeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { analyzeRun(t, p, b, b.DB, s) }
		run(fixture.Exec("CREATE TABLE analyze_invariant(a INT,b TEXT)", 0))
		run(fixture.Exec("INSERT INTO analyze_invariant VALUES(1,'a'),(1,'a'),(2,'b'),(NULL,'n')", 4))
		for _, maintenance := range []string{"", "ANALYZE analyze_invariant"} {
			if maintenance != "" {
				run(fixture.Exec(maintenance, 0))
			}
			for _, prepared := range []bool{false, true} {
				for _, s := range []fixture.Step{
					fixture.Select("SELECT a,b FROM analyze_invariant ORDER BY a NULLS LAST,b", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(1), "a"}, {int64(2), "b"}, {nil, "n"}}),
					fixture.Select("SELECT a FROM analyze_invariant WHERE a=1 ORDER BY a", []string{"a"}, []string{"INT4"}, analyzeCountRows(2, int64(1))),
					fixture.Select("SELECT a FROM analyze_invariant WHERE a>1 ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}}),
					fixture.Select("SELECT b FROM analyze_invariant WHERE a IS NULL", []string{"b"}, []string{"TEXT"}, [][]any{{"n"}}),
				} {
					s.Prepared = prepared
					run(s)
				}
			}
		}
		for _, arg := range []any{int64(1), int64(2)} {
			lines := explainQuery(t, p, b, "EXPLAIN SELECT a FROM analyze_invariant WHERE a=$1", true, arg)
			if !b.PG && analyzePlanEstimate(t, lines) != 2 {
				t.Fatalf("parameter inspected during estimation: %v", lines)
			}
		}
		run(fixture.Exec("CREATE TABLE analyze_empty(a INT)", 0))
		run(fixture.Exec("ANALYZE analyze_empty", 0))
		analyzeTuples(t, p, b, b.DB, "analyze_empty", 0)
		run(fixture.Exec("DELETE FROM analyze_invariant", 4))
		run(fixture.Exec("ANALYZE analyze_invariant", 0))
		analyzeTuples(t, p, b, b.DB, "analyze_invariant", 0)
		// Empty collection retains old distributions. VACUUM updates only the
		// population, making that old fraction publicly distinguishable again.
		run(fixture.Exec("INSERT INTO analyze_invariant VALUES(2,'x'),(2,'x'),(2,'x'),(2,'x')", 4))
		run(fixture.Exec("VACUUM analyze_invariant", 0))
		analyzeWantEstimate(t, p, b.DB, "analyze_invariant", "a=1", 2)
		run(fixture.Exec("DELETE FROM analyze_invariant", 4))
		run(fixture.Exec("INSERT INTO analyze_invariant VALUES(NULL,'new'),(NULL,'new')", 2))
		run(fixture.Exec("ANALYZE analyze_invariant(a)", 0))
		analyzeWantEstimate(t, p, b.DB, "analyze_invariant", "a IS NULL", 2)
	})
}
