package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// Cumulative counters can await PostgreSQL's backend flush. Each read is a new
// autocommit snapshot, matching the existing maintenance-counter convention.
func analyzeSuccessfulCount(t *testing.T, p *fixture.Pair, c *sql.Conn, table string, want int64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
	defer cancel()
	for {
		var got int64
		var recorded bool
		err := c.QueryRowContext(ctx, "SELECT analyze_count,last_analyze IS NOT NULL FROM pg_stat_all_tables WHERE relid=$1::regclass", table).Scan(&got, &recorded)
		if err != nil {
			t.Fatal(err)
		}
		if got == want && recorded {
			return
		}
		if ctx.Err() != nil {
			t.Fatalf("%s analyze_count=%d recorded=%t, want %d/true", table, got, recorded, want)
		}
		runtime.Gosched()
	}
}

func TestReferenceAnalyzeTruncateOrdering(t *testing.T) {
	for _, ordering := range []string{"truncate-before-analyze", "truncate-after-analyze", "rollback-analyze-savepoint", "rollback-truncate-savepoint"} {
		for _, insert := range []bool{false, true} {
			// The savepoint variants exercise two actual inserted rows.
			if !insert && (ordering == "rollback-analyze-savepoint" || ordering == "rollback-truncate-savepoint") {
				continue
			}
			for _, prepared := range []bool{false, true} {
				for _, preparedCompletion := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/insert=%t/prepared=%t/two-phase=%t", ordering, insert, prepared, preparedCompletion), func(t *testing.T) {
						analyzeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
							c := analyzeConn(t, p, b)
							run := func(s fixture.Step) { analyzeRun(t, p, b, c, s) }
							analyze := func() {
								s := fixture.Exec("ANALYZE analyze_truncate", 0)
								s.Prepared = prepared
								run(s)
							}
							run(fixture.Exec("CREATE TABLE analyze_truncate(a INT)", 0))
							run(fixture.Exec("INSERT INTO analyze_truncate VALUES(1),(1),(2)", 3))
							analyze()
							analyzeTuples(t, p, b, c, "analyze_truncate", 3)
							gid := fmt.Sprintf("analyze_order_%s_%t_%t", ordering, insert, prepared)
							if preparedCompletion {
								fixture.PreparedReferenceCleanup(t, b, gid)
							}
							run(fixture.Exec("BEGIN", 0))
							defer c.ExecContext(p.Ctx, "ROLLBACK")
							if ordering == "rollback-truncate-savepoint" {
								run(fixture.Exec("SAVEPOINT before_truncate", 0))
							}
							if ordering == "truncate-after-analyze" {
								analyze()
							}
							run(fixture.Exec("TRUNCATE analyze_truncate", 0))
							if insert {
								run(fixture.Exec("INSERT INTO analyze_truncate VALUES(4),(4)", 2))
							}
							if ordering == "rollback-analyze-savepoint" {
								run(fixture.Exec("SAVEPOINT before_analyze", 0))
							}
							if ordering != "truncate-after-analyze" {
								analyze()
							}
							if ordering == "rollback-analyze-savepoint" {
								run(fixture.Exec("ROLLBACK TO SAVEPOINT before_analyze", 0))
							}
							if ordering == "rollback-truncate-savepoint" {
								run(fixture.Exec("ROLLBACK TO SAVEPOINT before_truncate", 0))
								analyze() // The restored original heap receives the final scan.
							}
							if preparedCompletion {
								run(fixture.Exec("PREPARE TRANSACTION "+fixture.PreparedReferenceLiteral(gid), 0))
								run(fixture.Exec("COMMIT PREPARED "+fixture.PreparedReferenceLiteral(gid), 0))
							} else {
								run(fixture.Exec("COMMIT", 0))
							}
							wantTuples := int64(0)
							wantRows := [][]any(nil)
							wantCount := int64(2)
							if insert {
								wantTuples, wantRows = 2, analyzeCountRows(2, int64(4))
							}
							if ordering == "truncate-after-analyze" {
								wantTuples = -1
							}
							if ordering == "rollback-truncate-savepoint" {
								wantTuples, wantRows, wantCount = 3, [][]any{{int64(1)}, {int64(1)}, {int64(2)}}, 3
							}
							analyzeTuples(t, p, b, c, "analyze_truncate", wantTuples)
							for _, route := range []bool{false, true} {
								s := fixture.Select("SELECT a FROM analyze_truncate ORDER BY a", []string{"a"}, []string{"INT4"}, wantRows)
								s.Prepared = route
								run(s)
							}
							wantEstimate := wantTuples
							if wantTuples == 0 && b.PG {
								wantEstimate = 1
							}
							if wantTuples == -1 {
								wantEstimate = 1000
								if b.PG {
									wantEstimate = 2550
								} // Pinned physical unknown-width heuristic, not native parity.
							}
							analyzeWantEstimate(t, p, c, "analyze_truncate", "", wantEstimate)
							if ordering == "truncate-before-analyze" && insert {
								analyzeWantEstimate(t, p, c, "analyze_truncate", "a=4", 2)
							}
							if ordering == "rollback-analyze-savepoint" {
								// Cardinality/count survive; the column distribution returns to
								// baseline, whose residual mass estimates an unseen 4 as one.
								analyzeWantEstimate(t, p, c, "analyze_truncate", "a=4", 1)
							}
							analyzeSuccessfulCount(t, p, c, "analyze_truncate", wantCount)
						})
					})
				}
			}
		}
	}
}

// Rewrites can create another private successor after TRUNCATE. These scenarios
// distinguish a real post-truncate scan from copied pre-truncate maintenance.
func TestReferenceAnalyzeTruncateRewriteOrdering(t *testing.T) {
	cases := []struct {
		name                       string
		actions                    []string
		rows                       [][]any
		finalType                  string
		nativeTuples, pgTuples     int64
		analyzeCount, filteredRows int64
	}{
		{
			name:    "scan_then_rewrite",
			actions: []string{"truncate", "insert", "analyze", "rewrite"},
			rows:    [][]any{{int64(4), int64(40)}, {int64(4), int64(40)}}, finalType: "INT8",
			nativeTuples: 2, pgTuples: -1, analyzeCount: 2, filteredRows: 2,
		},
		{
			name:    "truncate_then_rewrite_without_scan",
			actions: []string{"truncate", "rewrite"}, finalType: "INT8",
			nativeTuples: -1, pgTuples: -1, analyzeCount: 1, filteredRows: -1,
		},
		{
			name:    "rewrite_scan_then_truncate",
			actions: []string{"rewrite", "analyze", "truncate"}, finalType: "INT8",
			nativeTuples: -1, pgTuples: 3, analyzeCount: 2, filteredRows: -1,
		},
		{
			name:    "rewrite_truncate_insert_scan",
			actions: []string{"rewrite", "truncate", "insert", "analyze"},
			rows:    [][]any{{int64(4), int64(40)}, {int64(4), int64(40)}}, finalType: "INT8",
			nativeTuples: 2, pgTuples: 2, analyzeCount: 2, filteredRows: 2,
		},
		{
			name:    "savepoint_rewrite_rollback",
			actions: []string{"truncate", "insert", "analyze", "savepoint", "rewrite", "insert_later", "rollback"},
			rows:    [][]any{{int64(4), int64(40)}, {int64(4), int64(40)}}, finalType: "INT4",
			nativeTuples: 2, pgTuples: 2, analyzeCount: 2, filteredRows: 2,
		},
		{
			name:    "savepoint_rewrite_release",
			actions: []string{"truncate", "insert", "analyze", "savepoint", "rewrite", "insert_later", "release"},
			rows:    [][]any{{int64(4), int64(40)}, {int64(4), int64(40)}, {int64(5), int64(50)}}, finalType: "INT8",
			nativeTuples: 2, pgTuples: -1, analyzeCount: 2, filteredRows: 2,
		},
	}
	for _, tc := range cases {
		for _, prepared := range []bool{false, true} {
			for _, preparedCompletion := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/prepared=%t/two-phase=%t", tc.name, prepared, preparedCompletion), func(t *testing.T) {
					analyzeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
						c := analyzeConn(t, p, b)
						run := func(s fixture.Step) { analyzeRun(t, p, b, c, s) }
						scan := func() {
							s := fixture.Exec("ANALYZE analyze_rewrite(a)", 0)
							s.Prepared = prepared
							run(s)
						}
						run(fixture.Exec("CREATE TABLE analyze_rewrite(a INT,b INT)", 0))
						run(fixture.Exec("INSERT INTO analyze_rewrite VALUES(1,10),(1,11),(2,20)", 3))
						scan()
						analyzeSuccessfulCount(t, p, c, "analyze_rewrite", 1)
						var baselineTime time.Time
						if err := c.QueryRowContext(p.Ctx, "SELECT last_analyze FROM pg_stat_all_tables WHERE relid='analyze_rewrite'::regclass").Scan(&baselineTime); err != nil {
							t.Fatal(err)
						}
						gid := fmt.Sprintf("analyze_rewrite_%s_%t", tc.name, prepared)
						if preparedCompletion {
							fixture.PreparedReferenceCleanup(t, b, gid)
						}
						run(fixture.Exec("BEGIN", 0))
						defer c.ExecContext(p.Ctx, "ROLLBACK")
						for _, action := range tc.actions {
							switch action {
							case "truncate":
								run(fixture.Exec("TRUNCATE analyze_rewrite", 0))
							case "insert":
								run(fixture.Exec("INSERT INTO analyze_rewrite VALUES(4,40),(4,40)", 2))
							case "insert_later":
								run(fixture.Exec("INSERT INTO analyze_rewrite VALUES(5,50)", 1))
							case "analyze":
								scan()
							case "rewrite":
								run(fixture.Exec("ALTER TABLE analyze_rewrite ALTER COLUMN b TYPE BIGINT", 0))
							case "savepoint":
								run(fixture.Exec("SAVEPOINT before_rewrite", 0))
							case "rollback":
								run(fixture.Exec("ROLLBACK TO SAVEPOINT before_rewrite", 0))
							case "release":
								run(fixture.Exec("RELEASE SAVEPOINT before_rewrite", 0))
							default:
								t.Fatalf("unrecognized rewrite action %q", action)
							}
						}
						if preparedCompletion {
							run(fixture.Exec("PREPARE TRANSACTION "+fixture.PreparedReferenceLiteral(gid), 0))
							run(fixture.Exec("COMMIT PREPARED "+fixture.PreparedReferenceLiteral(gid), 0))
						} else {
							run(fixture.Exec("COMMIT", 0))
						}
						for _, route := range []bool{false, true} {
							s := fixture.Select("SELECT a,b FROM analyze_rewrite ORDER BY a,b", []string{"a", "b"}, []string{"INT4", tc.finalType}, tc.rows)
							s.Prepared = route
							run(s)
						}
						tuples := tc.nativeTuples
						if b.PG {
							tuples = tc.pgTuples
						}
						analyzeTuples(t, p, b, c, "analyze_rewrite", tuples)
						physicalPlan := func(predicate string) {
							query := "EXPLAIN SELECT * FROM analyze_rewrite"
							if predicate != "" {
								query += " WHERE " + predicate
							}
							lines := explainQuery(t, p, b, query, prepared)
							text := strings.Join(lines, "\n")
							if analyzePlanEstimate(t, lines) <= 0 || !strings.Contains(text, "Seq Scan on analyze_rewrite") {
								t.Fatalf("expected real scan with positive physical estimate: %v", lines)
							}
							if predicate != "" && !strings.Contains(text, "= 4") {
								t.Fatalf("physical plan omits the independent a=4 restriction: %v", lines)
							}
						}
						if b.PG && (tuples < 0 || tc.name == "rewrite_scan_then_truncate") {
							// Pinned PostgreSQL rewrites can discard or retain old
							// cardinality independently of current physical pages.
							// Do not infer physical scan estimates from reltuples.
							physicalPlan("")
						} else if tuples >= 0 {
							estimate := tuples
							if b.PG && estimate == 0 {
								estimate = 1
							}
							analyzeWantEstimate(t, p, c, "analyze_rewrite", "", estimate)
						} else {
							analyzeWantEstimate(t, p, c, "analyze_rewrite", "", 1000)
						}
						if tc.filteredRows >= 0 {
							// Native preserves compatible a facts through b's rewrite.
							// PostgreSQL unknown-cardinality plans have independently
							// checked scan/restriction structure, not native numeric parity.
							if b.PG && tuples < 0 {
								physicalPlan("a=4")
							} else {
								analyzeWantEstimate(t, p, c, "analyze_rewrite", "a=4", tc.filteredRows)
							}
						}
						analyzeSuccessfulCount(t, p, c, "analyze_rewrite", tc.analyzeCount)
						var completedTime time.Time
						if err := c.QueryRowContext(p.Ctx, "SELECT last_analyze FROM pg_stat_all_tables WHERE relid='analyze_rewrite'::regclass").Scan(&completedTime); err != nil {
							t.Fatal(err)
						}
						if tc.analyzeCount == 1 {
							if !completedTime.Equal(baselineTime) {
								t.Fatalf("rewrite invented ANALYZE time: %v -> %v", baselineTime, completedTime)
							}
						} else if !completedTime.After(baselineTime) {
							t.Fatalf("successful collection did not advance ANALYZE time: %v -> %v", baselineTime, completedTime)
						}
					})
				})
			}
		}
	}
}
