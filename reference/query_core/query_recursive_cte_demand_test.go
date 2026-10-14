package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryRecursiveCTEDemand(t *testing.T) {
	recursiveCTEReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		for _, op := range []string{"UNION ALL", "UNION"} {
			q := "WITH RECURSIVE r(n) AS (SELECT 1 " + op + " SELECT n+1 FROM r) SELECT n FROM r LIMIT 3"
			recursiveCTERoutes(t, p, b, c, cteBag(q, []string{"n"}, []string{"INT4"}, recursiveCTEInts(1, 2, 3)))
		}
		empty := cteBag("WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n FROM r) SELECT n FROM r LIMIT 0", []string{"n"}, []string{"INT4"}, nil)
		empty.CheckMetadata = recursiveCTEMetadata(b, empty.Columns, empty.Types)
		// Sequence effects below supply the independent no-open proof.
		recursiveCTERoutes(t, p, b, c, empty)
		for i, hint := range []string{"", "MATERIALIZED", "NOT MATERIALIZED"} {
			for _, prepared := range []bool{false, true} {
				name := []string{"recursive_demand_a", "recursive_demand_b", "recursive_demand_c"}[i]
				run(fixture.Exec("CREATE SEQUENCE "+name, 0))
				q := "WITH RECURSIVE r(n) AS " + hint + " (SELECT nextval('" + name + "') UNION ALL SELECT nextval('" + name + "') FROM r) SELECT n FROM r LIMIT 1"
				var x fixture.Executor = c
				if prepared {
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					x = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				}
				// Prepare and an unused recursive item are effect-free.
				run(cteBag("WITH RECURSIVE r(n) AS (SELECT nextval('"+name+"') UNION ALL SELECT n FROM r) SELECT 7 AS n", []string{"n"}, []string{"INT4"}, recursiveCTEInts(7)))
				run(cteBag("WITH RECURSIVE r(n) AS (SELECT nextval('"+name+"') UNION ALL SELECT n FROM r) SELECT n FROM r LIMIT 0", []string{"n"}, []string{"INT8"}, nil))
				fixture.RunStep(t, p.Ctx, x, b.Recorder, cteBag(q, []string{"n"}, []string{"INT8"}, recursiveCTEInts(1)))
				run(fixture.Select("SELECT nextval('"+name+"') AS n", []string{"n"}, []string{"INT8"}, recursiveCTEInts(2)))
				if prepared {
					fixture.RunStep(t, p.Ctx, x, b.Recorder, cteBag(q, []string{"n"}, []string{"INT8"}, recursiveCTEInts(3)))
					run(fixture.Select("SELECT nextval('"+name+"') AS n", []string{"n"}, []string{"INT8"}, recursiveCTEInts(4)))
				}
				run(fixture.Exec("DROP SEQUENCE "+name, 0))
			}
		}
		run(fixture.Exec("CREATE SEQUENCE recursive_shared", 0))
		for _, hint := range []string{"", "MATERIALIZED", "NOT MATERIALIZED"} {
			run(fixture.Exec("ALTER SEQUENCE recursive_shared RESTART WITH 1", 0))
			q := "WITH RECURSIVE r(n,depth) AS " + hint + " (SELECT nextval('recursive_shared'),1 UNION ALL SELECT nextval('recursive_shared'),depth+1 FROM r WHERE depth<3) SELECT a.n,b.n FROM r a CROSS JOIN r b"
			var want [][]any
			for a := int64(1); a <= 3; a++ {
				for d := int64(1); d <= 3; d++ {
					want = append(want, []any{a, d})
				}
			}
			run(cteBag(q, []string{"n", "n"}, []string{"INT8", "INT8"}, want))
			run(fixture.Select("SELECT nextval('recursive_shared') AS n", []string{"n"}, []string{"INT8"}, recursiveCTEInts(4)))
		}
		subqueryBoth(run, fixture.Select("WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM r WHERE n<3) SELECT sum(n) AS total FROM r", []string{"total"}, []string{"INT8"}, recursiveCTEInts(6)))
		subqueryBoth(run, cteBag("WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM r WHERE n<3) SELECT (SELECT sum(n) FROM r) AS a,(SELECT sum(n) FROM r) AS b", []string{"a", "b"}, []string{"INT8", "INT8"}, [][]any{{int64(6), int64(6)}}))
	})
}
