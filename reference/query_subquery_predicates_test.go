package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceQuerySubqueryPredicates(t *testing.T) {
	subqueryReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		subqueryReductionFixture(run)
		for _, tc := range []struct {
			expr  string
			value any
		}{
			{"EXISTS(SELECT v FROM r)", true}, {"EXISTS(SELECT v FROM r WHERE FALSE)", false},
			{"EXISTS(SELECT FROM r)", true}, {"EXISTS(SELECT FROM r WHERE FALSE)", false},
			{"EXISTS(SELECT v,v FROM r)", true}, {"EXISTS(SELECT 1/0 FROM r)", true},
			{"1 IN (SELECT v FROM r)", true}, {"3 IN (SELECT v FROM r)", nil},
			{"3 IN (SELECT v FROM r WHERE v IS NOT NULL)", false},
			{"1 NOT IN (SELECT v FROM r)", false}, {"3 NOT IN (SELECT v FROM r)", nil},
			{"3 NOT IN (SELECT v FROM r WHERE v IS NOT NULL)", true},
			{"CAST(NULL AS INT) IN (SELECT v FROM r WHERE FALSE)", false},
			{"CAST(NULL AS INT) NOT IN (SELECT v FROM r WHERE FALSE)", true},
			{"3 > ANY(SELECT v FROM r)", true}, {"3 > SOME(SELECT v FROM r)", true},
			{"0 > ANY(SELECT v FROM r)", nil}, {"3 > ALL(SELECT v FROM r)", nil},
			{"0 > ALL(SELECT v FROM r)", false},
			{"CAST(NULL AS INT) = ANY(SELECT v FROM r WHERE FALSE)", false},
			{"CAST(NULL AS INT) = ALL(SELECT v FROM r WHERE FALSE)", true},
			{"CAST(NULL AS INT) = ANY(SELECT CAST(NULL AS INT))", nil},
			{"CAST(NULL AS INT) = ALL(SELECT CAST(NULL AS INT))", nil},
			{"CAST(2 AS BIGINT) = ANY(SELECT v FROM r)", true},
			{"2 <> ALL(SELECT v FROM r)", false}, {"1 < ANY(SELECT v FROM r)", true},
			{"1 <= ANY(SELECT v FROM r)", true}, {"2 >= ALL(SELECT v FROM r WHERE v IS NOT NULL)", true},
		} {
			t.Run(tc.expr, func(t *testing.T) {
				subqueryBoth(subqueryRun(t, p, b, c), referenceSelect("SELECT "+tc.expr+" AS answer", []string{"answer"}, []string{"BOOL"}, [][]any{{tc.value}}))
			})
		}
		subqueryBoth(run, referenceSelect("SELECT EXISTS(SELECT v FROM r)", []string{"exists"}, []string{"BOOL"}, [][]any{{true}}))
		subqueryBoth(run, referenceSelect("SELECT v FROM r WHERE v IN (SELECT v FROM r WHERE v=1)", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
		subqueryBoth(run, referenceSelect("SELECT v FROM r WHERE v NOT IN (SELECT v FROM r WHERE v=1) ORDER BY v OFFSET 1 ROW FETCH FIRST ROW ONLY", []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}}))
		subqueryBoth(run, referenceSelect("SELECT a.v,b.v FROM r a JOIN r b ON a.v=b.v AND EXISTS(SELECT v FROM r WHERE v=a.v) ORDER BY a.v,b.v", []string{"v", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(2), int64(2)}, {int64(2), int64(2)}, {int64(2), int64(2)}}))
		subqueryBoth(run, referenceSelect("SELECT v,count(*) FROM r GROUP BY v HAVING EXISTS(SELECT 1) ORDER BY v LIMIT (SELECT 2) OFFSET (SELECT 0)", []string{"v", "count"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}}))
		subqueryBoth(run, referenceSelect("SELECT v FROM r ORDER BY (SELECT 1),v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(2)}, {nil}}))
	})
}

func TestReferenceQuerySubquerySmoke(t *testing.T) {
	subqueryReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		subqueryReductionFixture(run)
		subqueryScopeFixture(run)
		subqueryBoth(run, referenceSelect("SELECT EXISTS(SELECT v FROM r) AS present,3 IN (SELECT v FROM r) AS member,(SELECT v FROM r WHERE v=1) AS scalar", []string{"present", "member", "scalar"}, []string{"BOOL", "BOOL", "INT4"}, [][]any{{true, nil, int64(1)}}))
		subqueryBoth(run, referenceSelect("SELECT ROW(1,2) = (SELECT 1,2) AS explicit,(1,2) IN (SELECT 1,2) AS implicit", []string{"explicit", "implicit"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
		subqueryBoth(run, referenceSelect("SELECT o.id,(SELECT i.v FROM inner_t i WHERE i.k=o.id AND i.v=10) AS s FROM outer_t o ORDER BY o.id", []string{"id", "s"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), nil}, {int64(3), nil}, {int64(3), nil}}))
		step := referenceSelect("SELECT d.x FROM (SELECT v AS x FROM r) d", []string{"x"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {nil}, {int64(2)}})
		step.bag = true
		subqueryBoth(run, step)
		subqueryBoth(run, referenceSelect("SELECT CAST(NULL AS INT) IN (SELECT v FROM r WHERE FALSE) AS any_empty,CAST(NULL AS INT) NOT IN (SELECT v FROM r WHERE FALSE) AS all_empty", []string{"any_empty", "all_empty"}, []string{"BOOL", "BOOL"}, [][]any{{false, true}}))
		subqueryBoth(run, referenceSelect("SELECT 1 AS unit", []string{"unit"}, []string{"INT4"}, [][]any{{int64(1)}}))
		subqueryQueryFailure(t, p, b, c, "SELECT (SELECT v FROM r WHERE v=2)", subqueryCardinality())
		run(referenceExec("CREATE TABLE t(id INT,v INT)", 0))
		run(referenceExec("INSERT INTO t VALUES(1,0),(2,0),(3,0)", 3))
		run(referenceExec("CREATE TABLE s(k INT,n INT)", 0))
		run(referenceExec("INSERT INTO s VALUES(1,101),(2,202)", 2))
		run(referencePrepared(referenceExec("UPDATE t x SET v=(SELECT s.n FROM s WHERE s.k=x.id)", 3)))
		run(referenceSelect("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(101)}, {int64(2), int64(202)}, {int64(3), nil}}))
	})
}

func TestReferenceQuerySubqueryHeldSmoke(t *testing.T) {
	subqueryReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		subqueryReductionFixture(run)
		q := "SELECT EXISTS(SELECT v FROM r) AS present,$1 IN (SELECT v FROM r) AS member,(SELECT v FROM r WHERE v=1) AS scalar"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
		for _, tc := range []struct {
			arg    int64
			member any
		}{{3, nil}, {1, true}} {
			referenceRunStep(t, p.ctx, held, b.recorder, referenceSelect(q, []string{"present", "member", "scalar"}, []string{"BOOL", "BOOL", "INT4"}, [][]any{{true, tc.member, int64(1)}}, tc.arg))
		}
	})
}
