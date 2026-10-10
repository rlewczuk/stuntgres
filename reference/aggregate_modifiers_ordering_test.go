package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceAggregateModifiersOrdering(t *testing.T) {
	aggregateModifiersScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep)) {
		run(referenceExec("CREATE TABLE am_order(g INT,id INT,v TEXT,k INT)", 0))
		run(referenceExec("INSERT INTO am_order VALUES(1,1,'a',NULL),(1,2,'b',1),(1,3,'c',2),(2,4,'d',NULL),(2,5,'e',1)", 5))
		aggregateModifiersBoth(run, referenceSelect("SELECT g,string_agg(v,',' ORDER BY id),string_agg(v,',' ORDER BY id DESC),string_agg(v,',' ORDER BY k,id),string_agg(v,',' ORDER BY k DESC,id),string_agg(v,',' ORDER BY k DESC NULLS LAST,id),string_agg(v,',' ORDER BY k NULLS FIRST,id) FROM am_order GROUP BY g ORDER BY g DESC", []string{"g", "string_agg", "string_agg", "string_agg", "string_agg", "string_agg", "string_agg"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(2), "d,e", "e,d", "e,d", "d,e", "e,d", "d,e"}, {int64(1), "a,b,c", "c,b,a", "b,c,a", "a,c,b", "c,b,a", "a,b,c"}}))
		aggregateModifiersBoth(run, referenceSelect("SELECT string_agg(v,',' ORDER BY 0,1,1.5,id) FROM am_order", []string{"string_agg"}, []string{"TEXT"}, [][]any{{"a,b,c,d,e"}}))
		aggregateModifiersBoth(run, referenceSelect("SELECT count(id ORDER BY id DESC),bool_and(id>0 ORDER BY id),bool_or(id<0 ORDER BY id),every(id>0 ORDER BY id),avg(id ORDER BY id),avg(id::float8 ORDER BY id),avg(INTERVAL '1 day' ORDER BY id),min(id ORDER BY id DESC),max(id ORDER BY id),any_value(id ORDER BY id) FROM am_order WHERE id=1", []string{"count", "bool_and", "bool_or", "every", "avg", "avg", "avg", "min", "max", "any_value"}, []string{"INT8", "BOOL", "BOOL", "BOOL", "NUMERIC", "FLOAT8", "INTERVAL", "INT4", "INT4", "INT4"}, [][]any{{int64(1), true, false, true, "1.00000000000000000000", float64(1), "1 day", int64(1), int64(1), int64(1)}}))
	})
}

func TestReferenceAggregateModifiersMatchingCoercions(t *testing.T) {
	aggregateModifiersScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep)) {
		run(referenceExec("CREATE TABLE am_match(v VARCHAR(4),n INT)", 0))
		run(referenceExec("INSERT INTO am_match VALUES('b',2),('a',1),('a',1)", 3))
		for _, q := range []string{"SELECT string_agg(DISTINCT v,',' ORDER BY v) FROM am_match", "SELECT string_agg(DISTINCT v::text,',' ORDER BY v::text) FROM am_match"} {
			aggregateModifiersBoth(run, referenceSelect(q, []string{"string_agg"}, []string{"TEXT"}, [][]any{{"a,b"}}))
		}
		aggregateModifiersBoth(run, referenceSelect("SELECT count(DISTINCT n+1 ORDER BY n+1),count(DISTINCT 1 ORDER BY 1) FROM am_match", []string{"count", "count"}, []string{"INT8", "INT8"}, [][]any{{int64(2), int64(1)}}))
		for _, tc := range []struct{ q, token string }{
			{"SELECT string_agg(DISTINCT v::text,',' ORDER BY v) FROM am_match", "v)"},
			{"SELECT string_agg(DISTINCT v,',' ORDER BY v::text) FROM am_match", "v::text)"},
			{"SELECT count(DISTINCT n ORDER BY n+1) FROM am_match", "n+1"},
			{"SELECT count(DISTINCT n ORDER BY 1) FROM am_match", "1)"},
		} {
			aggregateModifiersBoth(run, aggregateModifiersError(tc.q, "42P10", "in an aggregate with DISTINCT, ORDER BY expressions must appear in argument list", tc.token))
		}
	})
}
