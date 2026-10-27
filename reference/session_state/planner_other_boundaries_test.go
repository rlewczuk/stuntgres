package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePlannerOtherBoundaries(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		s.plannerCostSeed()
		const query = "SELECT n,s FROM planner_cost_rows WHERE n=$1"
		held, err := s.conn.PrepareContext(s.p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		for _, mode := range []string{"auto", "force_generic_plan", "force_custom_plan"} {
			s.plannerCostSet("plan_cache_mode", mode, false)
			for _, tc := range []struct {
				arg  any
				rows [][]any
			}{{2, [][]any{{int64(2), "b"}, {int64(2), "b"}}}, {1, [][]any{{int64(1), "a"}}}, {nil, nil}} {
				step := fixture.Select(query, []string{"n", "s"}, []string{"INT4", "TEXT"}, tc.rows, tc.arg)
				step.Bag = true
				fixture.RunStep(t, s.p.Ctx, fixture.HeldStatement{Executor: s.conn, Statement: held, SQL: query}, s.recorder, step)
			}
		}
		for _, collapse := range []string{"1", "8"} {
			for _, jit := range []string{"off", "on"} {
				for _, name := range []string{"from_collapse_limit", "join_collapse_limit"} {
					s.plannerCostSet(name, collapse, false)
				}
				s.plannerCostSet("jit", jit, false)
				for _, q := range []string{"SELECT n,s FROM (SELECT n,s FROM planner_cost_rows) d WHERE n=2", "SELECT l.n,l.s FROM planner_cost_rows l JOIN (VALUES(2)) r(n) ON l.n=r.n"} {
					for _, prepared := range []bool{false, true} {
						step := plannerCostBag(q)
						step.Prepared = prepared
						s.run(step)
					}
					if !s.pg {
						plan := s.plannerOtherExplain(q, false)
						for _, fiction := range []string{"JIT:", "GEQO", "Workers Planned", "Generic Plan", "Custom Plan"} {
							if strings.Contains(plan, fiction) {
								t.Fatal(fiction, plan)
							}
						}
					}
				}
				for _, q := range []string{"SELECT n,s FROM (SELECT n,s FROM planner_cost_rows) d", "SELECT l.n,l.s FROM planner_cost_rows l LEFT JOIN (VALUES(2)) r(n) ON l.n=r.n"} {
					for _, prepared := range []bool{false, true} {
						step := fixture.Select(q, []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "b"}, {int64(1), "a"}, {int64(2), "b"}, {nil, nil}, {int64(3), "c"}})
						step.Bag = true
						step.Prepared = prepared
						s.run(step)
					}
				}
			}
		}
	})
}
