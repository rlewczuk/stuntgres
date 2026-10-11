package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryCTEFolding(t *testing.T) {
	cteReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		run(fixture.Exec("CREATE TABLE t(id INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(0),(1)", 2))
		division := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
		for _, hint := range []string{"", "MATERIALIZED", "NOT MATERIALIZED"} {
			q := "WITH c AS " + hint + " (SELECT id,10/id AS v FROM t) SELECT v FROM c WHERE id<>0"
			if hint == "MATERIALIZED" {
				cteExecutionFailure(t, p, b, c, q, division)
			} else {
				subqueryBoth(run, cteBag(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
			}
			q = "WITH c AS " + hint + " (SELECT id,10/id AS v FROM t) SELECT a.v,b.v FROM c a JOIN c b ON a.id=b.id WHERE a.id<>0 AND b.id<>0"
			if hint == "NOT MATERIALIZED" {
				subqueryBoth(run, cteBag(q, []string{"v", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(10), int64(10)}}))
			} else {
				cteExecutionFailure(t, p, b, c, q, division)
			}
		}
		// Folding preserves demand-changing fences; safe key restrictions may
		// still cross DISTINCT/group/set nodes, so these depend on computed slots.
		for _, q := range []string{
			"WITH c AS NOT MATERIALIZED (SELECT id,10/id AS v FROM t LIMIT 2) SELECT v FROM c WHERE id<>0",
			"WITH c AS NOT MATERIALIZED (SELECT DISTINCT 10/id AS v FROM t) SELECT v FROM c WHERE v=10",
			"WITH c AS NOT MATERIALIZED (SELECT sum(10/id) AS v FROM t) SELECT v FROM c WHERE v=10",
			"WITH c AS NOT MATERIALIZED (SELECT id,10/id AS v,row_number() OVER (ORDER BY id) AS rn FROM t) SELECT v FROM c WHERE rn>0",
			"WITH c AS NOT MATERIALIZED (SELECT 10/id AS v FROM t UNION SELECT 10/id AS v FROM t) SELECT v FROM c WHERE v=10",
		} {
			cteExecutionFailure(t, p, b, c, q, division)
		}
		// Outer-join restriction is not a license to remove NULL-extension.
		subqueryBoth(run, cteBag("WITH c AS NOT MATERIALIZED (SELECT id,10/id AS v FROM t WHERE id<>0) SELECT t.id,c.v FROM t LEFT JOIN c ON t.id=c.id AND c.id<>0", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(0), nil}, {int64(1), int64(10)}}))
		// STABLE, unlike VOLATILE, does not prevent restriction folding.
		subqueryBoth(run, cteBag("WITH c AS NOT MATERIALIZED (SELECT id,10/id AS v,current_schema AS s FROM t) SELECT v FROM c WHERE id<>0", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
		// Positive controls retain cardinality/window coordinates after folding.
		subqueryBoth(run, cteBag("WITH c AS NOT MATERIALIZED (SELECT DISTINCT id FROM t) SELECT id FROM c WHERE id=1", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		subqueryBoth(run, cteBag("WITH c AS NOT MATERIALIZED (SELECT id,count(*) AS n FROM t GROUP BY id) SELECT n FROM c WHERE id=1", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
		subqueryBoth(run, cteBag("WITH c AS NOT MATERIALIZED (SELECT id,row_number() OVER (ORDER BY id) AS rn FROM t) SELECT rn FROM c WHERE id=1", []string{"rn"}, []string{"INT8"}, [][]any{{int64(2)}}))
		subqueryBoth(run, cteBag("WITH c AS NOT MATERIALIZED (SELECT id FROM t UNION SELECT id FROM t) SELECT id FROM c WHERE id=1", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		subqueryBoth(run, cteBag("WITH c AS NOT MATERIALIZED (SELECT DISTINCT id,10/id AS v FROM t) SELECT v FROM c WHERE id<>0", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
		subqueryBoth(run, cteBag("WITH c AS NOT MATERIALIZED (SELECT id,sum(10/id) AS v FROM t GROUP BY id) SELECT v FROM c WHERE id<>0", []string{"v"}, []string{"INT8"}, [][]any{{int64(10)}}))
		subqueryBoth(run, cteBag("WITH c AS NOT MATERIALIZED (SELECT id,10/id AS v FROM t UNION SELECT id,10/id AS v FROM t) SELECT v FROM c WHERE id<>0", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
		subqueryBoth(run, cteBag("WITH c AS NOT MATERIALIZED (SELECT id,10/id AS v FROM t) SELECT a.v,b.v FROM c a JOIN c b ON a.id=b.id AND b.id<>0 WHERE a.id<>0", []string{"v", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(10), int64(10)}}))
		subqueryBoth(run, cteBag("WITH a AS NOT MATERIALIZED (SELECT id,10/id AS v FROM t), b AS NOT MATERIALIZED (SELECT id,v FROM a) SELECT v FROM b WHERE id<>0", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
	})
}

func TestReferenceQueryCTEVolatility(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			cteReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := subqueryRun(t, p, b, c)
				for i, hint := range []string{"", "MATERIALIZED", "NOT MATERIALIZED"} {
					name := []string{"s_default", "s_materialized", "s_inline"}[i]
					run(fixture.Exec("CREATE SEQUENCE "+name, 0))
					q := "WITH c AS " + hint + " (SELECT nextval('" + name + "') AS n) SELECT a.n,b.n FROM c a CROSS JOIN c b"
					var x fixture.Executor = c
					if prepared {
						stmt, err := c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						x = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
					}
					for _, n := range []int64{1, 2} {
						fixture.RunStep(t, p.Ctx, x, b.Recorder, cteBag(q, []string{"n", "n"}, []string{"INT8", "INT8"}, [][]any{{n, n}}))
					}
					run(cteBag("SELECT nextval('"+name+"') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}))
				}
				run(fixture.Exec("CREATE SEQUENCE hidden_s", 0))
				run(fixture.Exec("CREATE TABLE t(id INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(0),(1)", 2))
				// Even an unused target or nested volatile call prevents folding.
				for _, expr := range []string{"nextval('hidden_s')", "(SELECT nextval('hidden_s'))"} {
					q := "WITH c AS NOT MATERIALIZED (SELECT id,10/id AS v," + expr + " AS ignored FROM t) SELECT v FROM c WHERE id<>0"
					cteExecutionFailure(t, p, b, c, q, fixture.ControlsReferenceDiagnostic("22012", "division by zero"))
				}
				// Static eligibility sees nested WITH volatility even when the
				// nested producer remains unused and makes no sequence draw.
				run(fixture.Exec("CREATE SEQUENCE unused_s", 0))
				for _, hint := range []string{"", "NOT MATERIALIZED"} {
					q := "WITH c AS " + hint + " (WITH ignored AS (SELECT nextval('unused_s') AS n) SELECT id,10/id AS v FROM t) SELECT v FROM c WHERE id<>0"
					cteExecutionFailure(t, p, b, c, q, fixture.ControlsReferenceDiagnostic("22012", "division by zero"))
				}
				run(cteBag("SELECT nextval('unused_s') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
			})
		})
	}
}

func TestReferenceQueryCTEDemand(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			cteReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				raw := subqueryRun(t, p, b, c)
				raw(fixture.Exec("CREATE TABLE t(id INT)", 0))
				raw(fixture.Exec("INSERT INTO t VALUES(1),(2)", 2))
				for _, tc := range []struct {
					name, q     string
					cols, types []string
					rows        [][]any
					next        int64
				}{
					{"unused", "WITH c AS MATERIALIZED (SELECT nextval('s')) SELECT 7 AS n", []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}}, 1},
					{"chain", "WITH c AS MATERIALIZED (SELECT nextval('s') AS n), d AS MATERIALIZED (SELECT n FROM c) SELECT 7 AS n", []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}}, 1},
					{"zero", "WITH c AS MATERIALIZED (SELECT nextval('s') AS n FROM t) SELECT n FROM c LIMIT 0", []string{"n"}, []string{"INT8"}, nil, 1},
					{"one", "WITH c AS MATERIALIZED (SELECT nextval('s') AS n FROM t) SELECT n FROM c LIMIT 1", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}, 2},
					{"replay", "WITH c AS MATERIALIZED (SELECT nextval('s') AS n FROM t) SELECT (SELECT n FROM c LIMIT 1) AS first,(SELECT count(*) FROM c) AS total,(SELECT n FROM c LIMIT 1) AS replay", []string{"first", "total", "replay"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(1), int64(2), int64(1)}}, 3},
					{"sort", "WITH c AS MATERIALIZED (SELECT nextval('s') AS n FROM t) SELECT n FROM c ORDER BY n DESC LIMIT 1", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}, 3},
				} {
					t.Run(tc.name, func(t *testing.T) {
						childRaw := subqueryRun(t, p, b, c)
						run := func(s fixture.Step) { s.Prepared = prepared; childRaw(s) }
						childRaw(fixture.Exec("CREATE SEQUENCE s", 0))
						run(cteBag(tc.q, tc.cols, tc.types, tc.rows))
						childRaw(cteBag("SELECT nextval('s') AS n", []string{"n"}, []string{"INT8"}, [][]any{{tc.next}}))
						childRaw(fixture.Exec("DROP SEQUENCE s", 0))
					})
				}
			})
		})
	}
}

func TestReferenceQueryCTEInvocation(t *testing.T) {
	cteReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		run(fixture.Exec("CREATE TABLE t(id INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1),(2)", 2))
		for _, q := range []string{
			"SELECT o.id,(WITH c AS MATERIALIZED (SELECT o.id AS n) SELECT n FROM c) AS n FROM t o ORDER BY o.id",
			"SELECT o.id,(WITH c AS MATERIALIZED (SELECT o.id AS n) SELECT n FROM (SELECT n FROM c) d) AS n FROM t o ORDER BY o.id",
			"SELECT o.id,(WITH c AS MATERIALIZED (SELECT o.id AS n) SELECT n FROM c UNION ALL SELECT n FROM c LIMIT 1) AS n FROM t o ORDER BY o.id",
		} {
			subqueryBoth(run, fixture.Select(q, []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}}))
		}
		run(fixture.Exec("CREATE SEQUENCE s", 0))
		q := "WITH c AS MATERIALIZED (SELECT nextval('s') AS n) SELECT (SELECT n FROM c) AS a,(SELECT n FROM c) AS b"
		for _, n := range []int64{1, 2} {
			s := cteBag(q, []string{"a", "b"}, []string{"INT8", "INT8"}, [][]any{{n, n}})
			s.Prepared = n == 2
			run(s)
		}
		run(cteBag("SELECT nextval('s') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}))
	})
}
