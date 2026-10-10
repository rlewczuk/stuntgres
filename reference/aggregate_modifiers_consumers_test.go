package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceAggregateModifiersOwnership(t *testing.T) {
	aggregateModifiersScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep)) {
		run(referenceExec("CREATE TABLE am_owner(v INT)", 0))
		run(referenceExec("INSERT INTO am_owner VALUES(1),(1),(2),(NULL)", 4))
		aggregateModifiersBoth(run, referenceSelect("SELECT (SELECT count(*) FILTER(WHERE o.v=1)) AS n FROM am_owner o", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
		aggregateModifiersBoth(run, referenceSelect("SELECT (SELECT count(o.v ORDER BY o.v)) AS n FROM am_owner o", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}))
		aggregateModifiersBoth(run, referenceSelect("SELECT count(v) FILTER(WHERE (SELECT TRUE)),count(DISTINCT (SELECT v)) FROM am_owner", []string{"count", "count"}, []string{"INT8", "INT8"}, [][]any{{int64(3), int64(2)}}))
		aggregateModifiersBoth(run, referenceSelect("SELECT v FROM am_owner GROUP BY v HAVING count(*) FILTER(WHERE v=1)>0 ORDER BY count(DISTINCT v)", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
		run(referenceExec("CREATE TABLE am_join(v INT)", 0))
		run(referenceExec("INSERT INTO am_join VALUES(1),(2)", 2))
		aggregateModifiersBoth(run, referenceSelect("SELECT count(DISTINCT v) FILTER(WHERE v=1),string_agg(v::text,',' ORDER BY v) FROM am_owner JOIN am_join USING(v)", []string{"count", "string_agg"}, []string{"INT8", "TEXT"}, [][]any{{int64(1), "1,1,2"}}))
	})
}

func TestReferenceAggregateModifiersConsumersAtomicity(t *testing.T) {
	aggregateModifiersScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep)) {
		run(referenceExec("CREATE TABLE am_source(id INT,v TEXT)", 0))
		run(referenceExec("INSERT INTO am_source VALUES(1,'a'),(0,'b')", 2))
		run(referenceExec("CREATE TABLE am_out(v TEXT)", 0))
		run(referenceExec("INSERT INTO am_out VALUES('sentinel')", 1))
		for _, expr := range []string{
			"string_agg(DISTINCT v,',' ORDER BY v) FILTER(WHERE 10/id>0)",
			"string_agg(DISTINCT (10/id)::text,',' ORDER BY (10/id)::text)",
			"string_agg(v,',' ORDER BY 10/id)",
		} {
			q := "INSERT INTO am_out SELECT " + expr + " FROM am_source"
			s := aggregateModifiersError(q, "22012", "division by zero", "")
			s.selectRows = false
			s.errorStage = "execute"
			aggregateModifiersBoth(run, s)
			run(referenceSelect("SELECT v FROM am_out", []string{"v"}, []string{"TEXT"}, [][]any{{"sentinel"}}))
		}
		aggregateModifiersBoth(run, referenceExec("INSERT INTO am_out SELECT string_agg(DISTINCT v,',' ORDER BY v) FILTER(WHERE id<>0) FROM am_source", 1))
		run(referenceSelect("SELECT v FROM am_out ORDER BY v", []string{"v"}, []string{"TEXT"}, [][]any{{"a"}, {"a"}, {"a"}, {"a"}, {"sentinel"}}))
		for _, prepared := range []bool{false, true} {
			q := "INSERT INTO am_source VALUES(9,'leaked'); SELECT count(DISTINCT *) FROM am_source"
			s := aggregateModifiersError(q, "42601", "syntax error at or near \"*\"", "*) FROM")
			s.selectRows = false
			s.prepared = prepared
			run(s)
		}
		run(referenceSelect("SELECT id FROM am_source ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(1)}}))
	})
}

func TestReferenceAggregateModifiersFeatureSmoke(t *testing.T) {
	aggregateModifiersScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep)) {
		run(referenceExec("CREATE TABLE am_smoke(v INT)", 0))
		run(referenceExec("INSERT INTO am_smoke VALUES(1),(1),(NULL),(2)", 4))
		aggregateModifiersBoth(run, referenceSelect("SELECT count(DISTINCT v),count(v) FILTER(WHERE v=1) FROM am_smoke", []string{"count", "count"}, []string{"INT8", "INT8"}, [][]any{{int64(2), int64(2)}}))
		run(referenceExec("CREATE TABLE am_words(id INT,v TEXT)", 0))
		run(referenceExec("INSERT INTO am_words VALUES(1,'a'),(2,'b'),(3,'c')", 3))
		aggregateModifiersBoth(run, referenceSelect("SELECT string_agg(v,',' ORDER BY id),string_agg(v,',' ORDER BY id DESC) FROM am_words", []string{"string_agg", "string_agg"}, []string{"TEXT", "TEXT"}, [][]any{{"a,b,c", "c,b,a"}}))
		q := "SELECT count(v) FILTER(WHERE $1) FROM am_smoke"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range []struct {
			arg  any
			want int64
		}{{true, 3}, {false, 0}, {nil, 0}} {
			referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}, b.recorder, referenceSelect(q, []string{"count"}, []string{"INT8"}, [][]any{{tc.want}}, tc.arg))
		}
		aggregateModifiersBoth(run, aggregateModifiersError("SELECT count(DISTINCT v ORDER BY v+1) FROM am_smoke", "42P10", "in an aggregate with DISTINCT, ORDER BY expressions must appear in argument list", "v+1"))
		run(referenceSelect("SELECT v FROM am_smoke ORDER BY v NULLS LAST", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(2)}, {nil}}))
	})
}
