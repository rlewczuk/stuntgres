package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceExplainAnalyzeParallelAggregate(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step) }
			run(fixture.Exec("CREATE TABLE ea_parallel(id integer)", 0))
			run(fixture.Exec("INSERT INTO ea_parallel VALUES(1),(2),(3),(4)", 4))
			for _, tc := range []struct{query string; names, types []string; values [][]any; count float64}{
				{"SELECT id%2 AS k,string_agg('x'::text,''::text) AS v FROM ea_parallel GROUP BY id%2 HAVING string_agg('x'::text,''::text) IS NOT NULL ORDER BY k", []string{"k","v"}, []string{"INT4","TEXT"}, [][]any{{int64(0),"xx"},{int64(1),"xx"}}, 2},
				{"SELECT id%2 AS k,string_agg('x'::text,''::text) AS v FROM ea_parallel GROUP BY id%2 HAVING string_agg('x'::text,''::text) IS NULL ORDER BY k", []string{"k","v"}, []string{"INT4","TEXT"}, nil, 0},
				{"SELECT string_agg('x'::text,''::text) AS v FROM ea_parallel", []string{"v"}, []string{"TEXT"}, [][]any{{"xxxx"}}, 1},
				{"SELECT string_agg('x'::text,''::text) AS v FROM ea_parallel WHERE id<0", []string{"v"}, []string{"TEXT"}, [][]any{{nil}}, 1},
				{"SELECT string_agg('x'::text,''::text) AS v FROM ea_parallel WHERE id<0 HAVING string_agg('x'::text,''::text) IS NOT NULL", []string{"v"}, []string{"TEXT"}, nil, 0},
				{"SELECT string_agg('x'::text,''::text) AS v FROM ea_parallel WHERE id<0 HAVING string_agg('x'::text,''::text) IS NULL", []string{"v"}, []string{"TEXT"}, [][]any{{nil}}, 1},
				{"SELECT id%2 AS k,string_agg('x'::text,''::text) AS v FROM ea_parallel WHERE id<0 GROUP BY id%2 ORDER BY k", []string{"k","v"}, []string{"INT4","TEXT"}, nil, 0},
				// Retain count as logical aggregate coverage, not as evidence
				// that the native executor admitted a partial-capable provider.
				{"SELECT id%2 AS k,count(*) AS n FROM ea_parallel GROUP BY id%2 HAVING count(*)>1 ORDER BY k", []string{"k","n"}, []string{"INT4","INT8"}, [][]any{{int64(0),int64(2)},{int64(1),int64(2)}}, 2},
				{"SELECT id%2 AS k,count(*) AS n FROM ea_parallel GROUP BY id%2 HAVING count(*)>9 ORDER BY k", []string{"k","n"}, []string{"INT4","INT8"}, nil, 0},
				{"SELECT count(*) AS n FROM ea_parallel WHERE id<0", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, 1},
				{"SELECT id%2 AS k,count(*) AS n FROM ea_parallel WHERE id<0 GROUP BY id%2 ORDER BY k", []string{"k","n"}, []string{"INT4","INT8"}, nil, 0},
			} {
				for _, prepared := range []bool{false,true} {
					step := fixture.Select(tc.query, tc.names, tc.types, tc.values)
					if prepared { step = fixture.Prepared(step) }
					run(step)
					query := "EXPLAIN ANALYZE " + tc.query
					if prepared {
						statement, err := b.DB.PrepareContext(p.Ctx, query)
						if err != nil { t.Fatal(err) }
						for range 2 {
							rows, err := statement.QueryContext(p.Ctx)
							if err != nil { t.Fatal(err) }
							analyzeRoot(t, explainPlanRows(t, rows), tc.count)
						}
						if err := statement.Close(); err != nil { t.Fatal(err) }
					} else { analyzeRoot(t, analyzeQuery(t,p,b.DB,query,false),tc.count) }
				}
			}
		})
	}
}
