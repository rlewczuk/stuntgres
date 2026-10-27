package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceExplainPipeline(t *testing.T) {
	cases := []struct {
		name, sql  string
		native, pg []string
	}{
		{"singleton", "SELECT 42 AS answer", []string{"Project", "Singleton", "42"}, []string{"Output:", "42"}},
		{"values", "VALUES (1),(2),(3),(4)", []string{"Values", "estimated rows=4"}, []string{"rows=", "Output:"}},
		{"filter-values", "SELECT v FROM (VALUES(1),(2),(3),(4)) x(v) WHERE v>1", []string{"Project", "Filter", "Values", "v", ">"}, []string{"Output:", ">"}},
		{"false-filter", "SELECT v FROM (VALUES(1),(2),(3),(4)) x(v) WHERE FALSE", []string{"Filter", "estimated rows=0"}, []string{"false"}},
		{"self-join", "SELECT a.id,b.id FROM explain_pipe a JOIN explain_pipe b ON a.id=b.id", []string{"Inner Join", "a.id", "b.id", "Logical Scan"}, []string{"a.id", "b.id", "explain_pipe"}},
		{"left", "SELECT a.id,b.id FROM explain_pipe a LEFT JOIN explain_pipe b ON a.id=b.id", []string{"Left Join", "a.id", "b.id"}, []string{"a.id", "b.id", "explain_pipe"}},
		{"right", "SELECT a.id,b.id FROM explain_pipe a RIGHT JOIN explain_pipe b ON a.id=b.id", []string{"Right Join", "a.id", "b.id"}, []string{"a.id", "b.id", "explain_pipe"}},
		{"full", "SELECT a.id,b.id FROM explain_pipe a FULL JOIN explain_pipe b ON a.id=b.id", []string{"Full Join", "a.id", "b.id"}, []string{"a.id", "b.id", "explain_pipe"}},
		{"cross", "SELECT a.id,b.id FROM explain_pipe a CROSS JOIN explain_pipe b", []string{"Cross Join", "a.id", "b.id"}, []string{"a.id", "b.id"}},
		{"lateral", "SELECT a.id,x.v FROM explain_pipe a CROSS JOIN LATERAL generate_series(1,a.id+1) x(v)", []string{"Join", "Function Scan", "Lateral", "a.id", "+"}, []string{"a.id", "generate_series"}},
		{"derived-alias", "SELECT x.k FROM (SELECT id FROM explain_pipe) x(k)", []string{"Derived Scan", "x.k", "explain_pipe"}, []string{"Output:", "explain_pipe"}},
		{"values-alias-self-join", "SELECT a.k,b.k FROM (VALUES(1),(2)) a(k) CROSS JOIN (VALUES(3),(4)) b(k)", []string{"Cross Join", "a.k", "b.k", "Values"}, []string{"Output:"}},
		{"cte-alias-self-join", "WITH x AS MATERIALIZED (SELECT id FROM explain_pipe) SELECT a.k,b.k FROM x a(k) JOIN x b(k) ON a.k=b.k", []string{"CTE", "a.k", "b.k", "explain_pipe"}, []string{"CTE x", "a.k", "b.k"}},
		{"function-implicit-alias", "SELECT g FROM generate_series(1,2) g", []string{"Function Scan", "g.g", "generate_series"}, []string{"generate_series", "Output:"}},
		{"derived", "SELECT x.id FROM (SELECT id FROM explain_pipe WHERE id>1) x", []string{"Derived Scan", "Filter", "explain_pipe"}, []string{"explain_pipe", ">"}},
		{"cte", "WITH x AS MATERIALIZED (SELECT id FROM explain_pipe) SELECT a.id,b.id FROM x a JOIN x b ON a.id=b.id", []string{"CTE", "x", "Join", "explain_pipe"}, []string{"CTE x", "explain_pipe"}},
		{"recursive", "WITH RECURSIVE walk(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM walk WHERE n<3) SELECT n FROM walk", []string{"Recursive Union", "Worktable Scan", "walk", "n"}, []string{"Recursive Union", "WorkTable Scan"}},
		{"search-cycle", "WITH RECURSIVE walk(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM walk WHERE n<3) SEARCH DEPTH FIRST BY n SET seq CYCLE n SET cyc USING path SELECT n,seq,cyc,path FROM walk", []string{"Recursive Union", "Worktable Scan", "SEARCH", "CYCLE"}, []string{"Recursive Union", "WorkTable Scan"}},
		{"aggregate", "SELECT v,count(*) FROM explain_pipe GROUP BY v HAVING count(*)>1", []string{"Aggregate", "Filter", "count", "v"}, []string{"count(*)", "v", ">"}},
		{"grouping-sets", "SELECT v,count(*) FROM explain_pipe GROUP BY GROUPING SETS ((v),())", []string{"Aggregate", "Grouping", "v", "count"}, []string{"count(*)", "v"}},
		{"empty-global", "SELECT count(*) FROM explain_pipe WHERE FALSE", []string{"Aggregate", "estimated rows=1", "Filter"}, []string{"count(*)", "false"}},
		{"distinct", "SELECT DISTINCT v FROM explain_pipe", []string{"Distinct", "v"}, []string{"v", "explain_pipe"}},
		{"distinct-on", "SELECT DISTINCT ON(v) v,id FROM explain_pipe ORDER BY v,id DESC", []string{"Distinct On", "Sort", "v", "DESC"}, []string{"v", "id", "explain_pipe"}},
		{"union-all", "SELECT id FROM explain_pipe UNION ALL SELECT id FROM explain_pipe", []string{"UNION ALL", "Logical Scan"}, []string{"explain_pipe", "rows="}},
		{"union", "VALUES(1),(2) UNION VALUES(2),(3)", []string{"UNION", "Values"}, []string{"rows="}},
		{"intersect", "VALUES(1),(2) INTERSECT VALUES(2),(3)", []string{"INTERSECT", "Values"}, []string{"rows="}},
		{"intersect-all", "VALUES(1),(2) INTERSECT ALL VALUES(2),(3)", []string{"INTERSECT ALL", "Values"}, []string{"rows="}},
		{"except", "VALUES(1),(2) EXCEPT VALUES(2),(3)", []string{"EXCEPT", "Values"}, []string{"rows="}},
		{"except-all", "VALUES(1),(2) EXCEPT ALL VALUES(2),(3)", []string{"EXCEPT ALL", "Values"}, []string{"rows="}},
		{"window", "SELECT id,row_number() OVER(PARTITION BY v ORDER BY id DESC ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) FROM explain_pipe", []string{"Window", "row_number", "v", "DESC", "ROWS"}, []string{"row_number() OVER", "explain_pipe"}},
		{"bounds", "SELECT id FROM explain_pipe ORDER BY id OFFSET 1 LIMIT 2", []string{"Limit", "Offset", "1", "2", "Sort"}, []string{"rows=", "explain_pipe"}},
		{"ties", "SELECT v FROM explain_pipe ORDER BY v FETCH FIRST 1 ROW WITH TIES", []string{"Limit", "WITH TIES", "Sort"}, []string{"rows=", "explain_pipe"}},
		{"rowlock", "SELECT id FROM explain_pipe FOR UPDATE OF explain_pipe NOWAIT", []string{"Row Lock", "UPDATE", "NOWAIT", "explain_pipe"}, []string{"LockRows", "explain_pipe"}},
		{"function-from", "SELECT g.n,g.ord FROM generate_series(1,3) WITH ORDINALITY g(n,ord)", []string{"Function Scan", "generate_series", "Ordinality"}, []string{"generate_series", "Output:"}},
		{"target-srf", "SELECT id,generate_series(1,2) FROM explain_pipe", []string{"Set Project", "generate_series", "Logical Scan"}, []string{"generate_series", "explain_pipe"}},
		{"target-srf-scalars", "SELECT id+1,generate_series(1,2) FROM explain_pipe", []string{"Set Project", "generate_series", "+", "Logical Scan"}, []string{"generate_series", "explain_pipe"}},
		{"nested-target-srf", "SELECT generate_series(1,generate_series(1,2)) FROM explain_pipe", []string{"Set Project", "generate_series", "Logical Scan"}, []string{"generate_series", "explain_pipe"}},
		{"scalar-subplan", "SELECT a.id,(SELECT max(b.id) FROM explain_pipe b WHERE b.v=a.v) FROM explain_pipe a", []string{"SubPlan", "Aggregate", "a.v", "b.v"}, []string{"SubPlan", "max(b.id)", "a.v"}},
		{"exists", "SELECT a.id FROM explain_pipe a WHERE EXISTS(SELECT 1 FROM explain_pipe b WHERE b.v=a.v)", []string{"Filter", "SubPlan", "a.v", "b.v"}, []string{"a.v", "b.v", "explain_pipe"}},
		{"inactive-exists", "SELECT EXISTS(SELECT DISTINCT id FROM explain_pipe ORDER BY id)", []string{"Singleton", "explain_pipe"}, []string{"explain_pipe"}},
		{"cached-order", "SELECT id+1 AS n FROM explain_pipe ORDER BY id+1", []string{"Project", "Sort", "+"}, []string{"Output:", "+"}},
		{"bounded-volatile", "SELECT id,random() FROM explain_pipe ORDER BY id LIMIT 1", []string{"Evaluate", "Sort", "random", "Limit"}, []string{"Output:", "random", "Limit"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE explain_pipe(id integer,v text)", 0))
					run(fixture.Exec("INSERT INTO explain_pipe VALUES(1,'a'),(2,'a'),(3,'b')", 3))
					for _, prepared := range []bool{false, true} {
						t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
							lines := explainQuery(t, p, b, "EXPLAIN (VERBOSE) "+c.sql, prepared)
							explainCosts(t, b, lines, true)
							if b.PG {
								explainContains(t, lines, c.pg...)
							} else {
								explainContains(t, lines, c.native...)
								if c.name == "inactive-exists" {
									text := strings.Join(lines, "\n")
									if strings.Contains(text, "Sort") || strings.Contains(text, "Distinct") {
										t.Errorf("presence-only plan claims inactive sort/distinct: %s", text)
									}
								}
							}
						})
					}
					run(fixture.Select("SELECT id,v FROM explain_pipe ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), "a"}, {int64(3), "b"}}))
				})
			}
		})
	}
}
