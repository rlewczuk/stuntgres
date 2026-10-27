package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAnalyzeHierarchy(t *testing.T) {
	analyzeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := analyzeConn(t, p, b)
		run := func(s fixture.Step) {
			if !t.Run(s.Query, func(t *testing.T) {
				analyzeRun(t, p, b, c, s)
			}) {
				t.FailNow()
			}
		}
		for _, q := range []string{"CREATE TABLE analyze_parent(a INT)", "CREATE TABLE analyze_child(extra TEXT) INHERITS(analyze_parent)"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO analyze_parent VALUES(1),(1)", 2))
		run(fixture.Exec("INSERT INTO analyze_child(a,extra) VALUES(2,'x'),(2,'y'),(2,'z')", 3))
		run(fixture.Exec("ANALYZE ONLY analyze_parent", 0))
		analyzeTuples(t, p, b, c, "analyze_parent", 2)
		analyzeTuples(t, p, b, c, "analyze_child", -1)
		analyzeWantEstimate(t, p, c, "ONLY analyze_parent", "a=1", 2)
		// Parent's inherited facts must be captured despite ONLY. Native can
		// use the inherited population directly; PostgreSQL's Append uses each
		// child's own estimates, still unknown until separately analyzed.
		if !b.PG {
			analyzeWantEstimate(t, p, c, "analyze_parent", "a=2", 3)
		}
		run(fixture.Exec("ANALYZE analyze_parent", 0))
		analyzeTuples(t, p, b, c, "analyze_child", 3)
		if b.PG {
			lines := explainQuery(t, p, b, "EXPLAIN SELECT * FROM analyze_parent WHERE a=2", false)
			found := false
			for _, line := range lines {
				if strings.Contains(line, "analyze_child") && analyzeRowsPattern.FindStringSubmatch(line) != nil {
					found = analyzePlanEstimate(t, []string{line}) == 3
				}
			}
			if !found || !strings.Contains(strings.Join(lines, "\n"), "= 2") {
				t.Fatalf("expected child rows3 and restriction: %v", lines)
			}
		} else {
			analyzeWantEstimate(t, p, c, "analyze_parent", "a=2", 3)
		}
		run(fixture.Exec("UPDATE analyze_child SET a=1", 3))
		run(fixture.Exec("ANALYZE ONLY analyze_parent", 0))
		analyzeWantEstimate(t, p, c, "analyze_child", "a=2", 3)
		run(fixture.Exec("ANALYZE analyze_parent", 0))
		analyzeWantEstimate(t, p, c, "analyze_child", "a=1", 3)
		// Diamond traversal must not count the shared leaf twice.
		for _, q := range []string{"CREATE TABLE analyze_left() INHERITS(analyze_parent)", "CREATE TABLE analyze_right() INHERITS(analyze_parent)", "CREATE TABLE analyze_diamond() INHERITS(analyze_left,analyze_right)"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO analyze_diamond VALUES(9),(9),(9),(9)", 4))
		run(fixture.Exec("ANALYZE analyze_parent", 0))
		run(fixture.Select("SELECT a FROM analyze_parent ORDER BY a", []string{"a"}, []string{"INT4"}, append(analyzeCountRows(5, int64(1)), analyzeCountRows(4, int64(9))...)))
		if !b.PG {
			analyzeWantEstimate(t, p, c, "analyze_parent", "a=9", 4)
		}
		for _, q := range []string{"CREATE TABLE analyze_partition(a INT) PARTITION BY RANGE(a)", "CREATE TABLE analyze_leaf PARTITION OF analyze_partition FOR VALUES FROM(0) TO(10)"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO analyze_partition VALUES(2),(2),(2),(2)", 4))
		run(fixture.Exec("ANALYZE ONLY analyze_partition", 0))
		analyzeTuples(t, p, b, c, "analyze_leaf", -1)
		if !b.PG {
			analyzeWantEstimate(t, p, c, "analyze_partition", "a=2", 4)
		}
		run(fixture.Exec("ANALYZE analyze_partition", 0))
		analyzeTuples(t, p, b, c, "analyze_leaf", 4)
		analyzeWantEstimate(t, p, c, "analyze_partition", "a=2", 4)
		run(fixture.Exec("CREATE TABLE analyze_mv_source(a INT)", 0))
		run(fixture.Exec("INSERT INTO analyze_mv_source VALUES(1),(1)", 2))
		run(fixture.Exec("CREATE MATERIALIZED VIEW analyze_mv AS SELECT a FROM analyze_mv_source", 2))
		run(fixture.Exec("INSERT INTO analyze_mv_source VALUES(2),(2),(2)", 3))
		run(fixture.Exec("ANALYZE analyze_mv", 0))
		analyzeTuples(t, p, b, c, "analyze_mv", 2)
		run(fixture.Select("SELECT a FROM analyze_mv ORDER BY a", []string{"a"}, []string{"INT4"}, analyzeCountRows(2, int64(1))))
		run(fixture.Exec("REFRESH MATERIALIZED VIEW analyze_mv", 0))
		run(fixture.Exec("ANALYZE analyze_mv", 0))
		analyzeTuples(t, p, b, c, "analyze_mv", 5)
		analyzeWantEstimate(t, p, c, "analyze_mv", "a=2", 3)
		// A virtual expression that would raise if evaluated is not sampled.
		run(fixture.Exec("CREATE TABLE analyze_virtual(a INT,y INT GENERATED ALWAYS AS (1/(a-a)) VIRTUAL)", 0))
		run(fixture.Exec("INSERT INTO analyze_virtual(a) VALUES(1),(2)", 2))
		run(fixture.Exec("ANALYZE analyze_virtual(y)", 0))
		analyzeTuples(t, p, b, c, "analyze_virtual", 2)
		run(fixture.Exec("CREATE TEMP TABLE analyze_own_temp(a INT)", 0))
		run(fixture.Exec("INSERT INTO analyze_own_temp VALUES(8),(8)", 2))
		run(fixture.Exec("ANALYZE analyze_own_temp", 0))
		analyzeTuples(t, p, b, c, "analyze_own_temp", 2)
		peer := analyzeConn(t, p, b)
		analyzeRun(t, p, b, peer, fixture.Exec("CREATE TEMP TABLE analyze_peer_temp(a INT)", 0))
		analyzeRun(t, p, b, peer, fixture.Exec("INSERT INTO analyze_peer_temp VALUES(7),(7),(7)", 3))
		t.Cleanup(func() {
			for _, q := range []string{"DROP FOREIGN TABLE IF EXISTS analyze_foreign", "DROP SERVER IF EXISTS analyze_server", "DROP FOREIGN DATA WRAPPER IF EXISTS analyze_fdw"} {
				if _, err := c.ExecContext(p.Ctx, q); err != nil {
					t.Errorf("cleanup %s: %v", q, err)
				}
			}
		})
		for _, q := range []string{"CREATE FOREIGN DATA WRAPPER analyze_fdw", "CREATE SERVER analyze_server FOREIGN DATA WRAPPER analyze_fdw", "CREATE FOREIGN TABLE analyze_foreign(a INT) SERVER analyze_server"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.ErrorDiagnostic("ANALYZE analyze_foreign", analyzeError("55000", `foreign-data wrapper "analyze_fdw" has no handler`)))
		// Omitted-target analysis excludes unsupported foreign sources.
		run(fixture.Exec("ANALYZE", 0))
		analyzeTuples(t, p, b, peer, "analyze_peer_temp", -1)
	})
}
