package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

type explainAnalyzeDB interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	PrepareContext(context.Context, string) (*sql.Stmt, error)
}

func analyzeQuery(t *testing.T, p *fixture.Pair, db explainAnalyzeDB, query string, prepared bool, args ...any) []string {
	t.Helper()
	var rows *sql.Rows
	var err error
	if prepared {
		statement, prepareErr := db.PrepareContext(p.Ctx, query)
		if prepareErr != nil { t.Fatal(prepareErr) }
		defer statement.Close()
		rows, err = statement.QueryContext(p.Ctx, args...)
	} else {
		rows, err = db.QueryContext(p.Ctx, query, args...)
	}
	if err != nil { t.Fatal(err) }
	return explainPlanRows(t, rows)
}

var analyzeActual = regexp.MustCompile(`actual (?:time=[^)]*? )?rows=([0-9.]+) loops=([0-9]+)`)
var analyzeTime = regexp.MustCompile(`(?:actual time=|Planning Time: |Execution Time: )([0-9]+(?:\.[0-9]+)?)(?:\.\.([0-9]+(?:\.[0-9]+)?))?`)

func analyzeRoot(t *testing.T, lines []string, rows float64) {
	t.Helper()
	match := analyzeActual.FindStringSubmatch(lines[0])
	if match == nil { t.Fatalf("root has no actual output: %v", lines) }
	got, err := strconv.ParseFloat(match[1], 64)
	if err != nil || got != rows || match[2] != "1" { t.Fatalf("root actual = %v; want rows=%v loops=1: %v", match, rows, lines) }
	for _, match := range analyzeTime.FindAllStringSubmatch(strings.Join(lines, "\n"), -1) {
		for _, value := range match[1:] {
			if value == "" { continue }
			elapsed, err := strconv.ParseFloat(value, 64)
			if err != nil || elapsed < 0 || math.IsNaN(elapsed) || math.IsInf(elapsed, 0) { t.Errorf("invalid measured duration %q", value) }
		}
	}
}

func TestReferenceExplainAnalyzeSelect(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE ea_t(id integer)", 0))
			run(fixture.Exec("INSERT INTO ea_t VALUES(1),(2),(3)", 3))
			run(fixture.Exec("CREATE SEQUENCE ea_s", 0))
			run(fixture.Exec("CREATE SEQUENCE ea_unused", 0))
			run(fixture.Exec("CREATE VIEW ea_v AS SELECT id FROM ea_t", 0))
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprint(prepared), func(t *testing.T) {
					for _, c := range []struct{ query string; rows float64 }{
						{"SELECT id FROM ea_t WHERE id<0", 0}, {"SELECT id FROM ea_t WHERE id=2", 1}, {"SELECT id FROM ea_t WHERE id>0", 3},
						{"VALUES(1),(2)", 2}, {"SELECT", 1},
						{"SELECT id+1 FROM ea_t ORDER BY id DESC", 3},
						{"SELECT a.id FROM ea_t a JOIN ea_t b ON a.id=b.id", 3},
						{"SELECT a.id FROM ea_t a LEFT JOIN ea_t b ON b.id=a.id+10", 3},
						{"SELECT a.id,x.v FROM ea_t a CROSS JOIN LATERAL generate_series(1,a.id) x(v)", 6},
						{"SELECT count(*) FROM ea_t", 1},
						{"SELECT id,count(*) FROM ea_t GROUP BY GROUPING SETS ((id),())", 4},
						{"SELECT DISTINCT id FROM ea_t", 3},
						{"SELECT DISTINCT ON (id) id FROM ea_t ORDER BY id", 3},
						{"SELECT id FROM ea_t UNION SELECT 4", 4},
						{"SELECT id FROM ea_t INTERSECT SELECT 2", 1},
						{"SELECT id FROM ea_t EXCEPT SELECT 2", 2},
						{"SELECT * FROM (SELECT id FROM ea_t) x", 3},
						{"SELECT * FROM (SELECT id FROM ea_t WHERE id<3) x", 2},
						{"WITH x AS MATERIALIZED (SELECT id FROM ea_t WHERE id<3) SELECT id FROM x UNION ALL SELECT id FROM x", 4},
						{"SELECT id FROM ea_t WHERE id<3 UNION ALL SELECT id FROM ea_t WHERE id<3", 4},
						{"WITH x AS MATERIALIZED (SELECT id FROM ea_t) SELECT a.id FROM x a JOIN x b ON a.id=b.id", 3},
						{"SELECT id FROM ea_v", 3},
						{"WITH unused AS (SELECT nextval('ea_unused')) SELECT id FROM ea_t", 3},
						{"SELECT EXISTS(SELECT nextval('ea_unused') FROM ea_t)", 1},
						{"WITH RECURSIVE x(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM x WHERE n<3) SELECT n FROM x", 3},
						{"SELECT id,row_number() OVER (ORDER BY id) FROM ea_t", 3},
						{"SELECT generate_series(1,id) FROM ea_t", 6},
						{"SELECT id FROM ea_t FOR UPDATE", 3},
						{"SELECT id FROM ea_t a WHERE EXISTS(SELECT 1 FROM ea_t b WHERE a.id=b.id)", 3},
					} {
						t.Run(c.query, func(t *testing.T) { analyzeRoot(t, analyzeQuery(t, p, b.DB, "EXPLAIN ANALYZE "+c.query, prepared), c.rows) })
					}
					for _, c := range []struct{ query string; values [][]any }{
						{"SELECT * FROM (SELECT id FROM ea_t WHERE id<3) x ORDER BY id", [][]any{{int64(1)},{int64(2)}}},
						{"WITH x AS MATERIALIZED (SELECT id FROM ea_t WHERE id<3) SELECT id FROM x UNION ALL SELECT id FROM x ORDER BY id", [][]any{{int64(1)},{int64(1)},{int64(2)},{int64(2)}}},
						{"SELECT id FROM ea_t WHERE id<3 UNION ALL SELECT id FROM ea_t WHERE id<3 ORDER BY id", [][]any{{int64(1)},{int64(1)},{int64(2)},{int64(2)}}},
					} {
						step := fixture.Select(c.query, []string{"id"}, []string{"INT4"}, c.values)
						if prepared { step = fixture.Prepared(step) }
						run(step)
					}
					statement, err := b.DB.PrepareContext(p.Ctx, "EXPLAIN ANALYZE SELECT id FROM ea_t WHERE id<=$1")
					if err != nil { t.Fatal(err) }
					defer statement.Close()
					for _, n := range []int{3, 0, 1, 2} {
						rows, err := statement.QueryContext(p.Ctx, n)
						if err != nil { t.Fatal(err) }
						analyzeRoot(t, explainPlanRows(t, rows), float64(n))
						analyzeRoot(t,analyzeQuery(t,p,b.DB,"EXPLAIN ANALYZE SELECT id FROM ea_t WHERE id<=$1",prepared,n),float64(n))
					}
					analyzeRoot(t, analyzeQuery(t, p, b.DB, "EXPLAIN ANALYZE SELECT nextval('ea_s') FROM ea_t", prepared), 3)
					analyzeRoot(t, analyzeQuery(t, p, b.DB, "EXPLAIN ANALYZE SELECT nextval('ea_s') FROM ea_t LIMIT 1", prepared), 1)
					last := int64(4)
					if prepared { last = 8 }
					run(fixture.Select("SELECT last_value,is_called FROM ea_s", []string{"last_value","is_called"}, []string{"INT8","BOOL"}, [][]any{{last,true}}))
				})
			}
			explainAnalyzePreparedLifetime(t,p,b)
			run(fixture.Select("SELECT is_called FROM ea_unused", []string{"is_called"}, []string{"BOOL"}, [][]any{{false}}))
		})
	}
}

func TestReferenceExplainAnalyzeDemand(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, q := range []string{"CREATE TABLE ea_o(id integer)", "CREATE TABLE ea_i(id integer)", "INSERT INTO ea_o VALUES(1),(2),(3)", "INSERT INTO ea_i VALUES(1),(2),(3)"} {
				if _, err := b.DB.ExecContext(p.Ctx, q); err != nil { t.Fatal(err) }
			}
			for _, prepared := range []bool{false, true} {
				for _, c := range []struct{ query string; rows float64 }{
					{"SELECT id FROM ea_o LIMIT 0", 0}, {"SELECT id FROM ea_o LIMIT 1", 1}, {"SELECT id FROM ea_o ORDER BY id DESC LIMIT 1", 1},
					{"SELECT a.id,b.id FROM ea_o a JOIN ea_o b ON a.id=b.id", 3},
				} { analyzeRoot(t, analyzeQuery(t, p, b.DB, "EXPLAIN ANALYZE "+c.query, prepared), c.rows) }
				for _, correlated := range []bool{false, true} {
					query := "EXPLAIN ANALYZE SELECT a.id,(SELECT count(*) FROM ea_i b"
					loops := "1"
					if correlated { query += " WHERE b.id<=a.id"; loops = "3" }
					query += ") FROM ea_o a"
					lines := analyzeQuery(t, p, b.DB, query, prepared)
					analyzeRoot(t, lines, 3)
					found := false
					for _, line := range lines {
						if strings.Contains(line, "Aggregate") {
							match := analyzeActual.FindStringSubmatch(line)
							if match != nil && match[1] != "0" && match[2] == loops { found = true }
						}
					}
					if !found { t.Errorf("aggregate must emit one row on each of %s loops: %v", loops, lines) }
				}
			}
		})
	}
}

func TestReferenceExplainAnalyzeOptions(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			if _, err := b.DB.ExecContext(p.Ctx, "CREATE SEQUENCE ea_disabled"); err != nil { t.Fatal(err) }
			for _, prepared := range []bool{false, true} {
				for _, prefix := range []string{"EXPLAIN ANALYZE", "EXPLAIN ANALYSE", "EXPLAIN (ANALYZE)", "EXPLAIN (ANALYZE FALSE, ANALYZE TRUE)", "EXPLAIN (ANALYZE, VERBOSE, COSTS OFF)", "EXPLAIN (ANALYZE, BUFFERS FALSE, SERIALIZE NONE)"} {
					lines := analyzeQuery(t, p, b.DB, prefix+" SELECT 1", prepared)
					analyzeRoot(t, lines, 1)
					for _, marker := range []string{"actual time=", "Planning Time:", "Execution Time:"} {
						if !strings.Contains(strings.Join(lines,"\n"),marker) { t.Errorf("default measurement omits %s: %v",marker,lines) }
					}
					if !b.PG {
						text := strings.Join(lines, "\n")
						if !strings.Contains(prefix,"COSTS OFF") {
							for _, marker := range []string{"estimated rows=","logical cost=","Estimates: logical heuristics; not execution measurements"} {
								if !strings.Contains(text,marker) { t.Errorf("unlabelled estimate: missing %s: %v",marker,lines) }
							}
						}
						for _, marker := range []string{"Buffers:", "WAL:"} { if strings.Contains(text, marker) { t.Errorf("invented %s: %v", marker, lines) } }
					}
				}
				lines := analyzeQuery(t, p, b.DB, "EXPLAIN (ANALYZE, TIMING OFF, SUMMARY OFF, COSTS OFF) SELECT 1", prepared)
				analyzeRoot(t, lines, 1)
				for _, marker := range []string{"actual time=", "Planning Time:", "Execution Time:", "cost=", "estimated rows="} {
					if strings.Contains(strings.Join(lines, "\n"), marker) { t.Errorf("disabled %s: %v", marker, lines) }
				}
				lines = analyzeQuery(t, p, b.DB, "EXPLAIN (SUMMARY ON) SELECT 1", prepared)
				text := strings.Join(lines, "\n")
				if !strings.Contains(text, "Planning Time:") || strings.Contains(text, "Execution Time:") || strings.Contains(text, "actual ") { t.Errorf("planning-only summary: %v", lines) }
				for _, prefix := range []string{"EXPLAIN (ANALYZE FALSE)", "EXPLAIN (ANALYZE TRUE, ANALYZE FALSE, TIMING FALSE, SUMMARY FALSE)"} {
					lines = analyzeQuery(t, p, b.DB, prefix+" SELECT nextval('ea_disabled')", prepared)
					if strings.Contains(strings.Join(lines, "\n"), "actual ") { t.Errorf("disabled ANALYZE executed: %v", lines) }
				}
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT is_called FROM ea_disabled", []string{"is_called"}, []string{"BOOL"}, [][]any{{false}}))
		})
	}
}
