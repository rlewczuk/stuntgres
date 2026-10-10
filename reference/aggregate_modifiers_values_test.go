package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceAggregateModifiersValues(t *testing.T) {
	aggregateModifiersScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep)) {
		run(referenceExec("CREATE TABLE am_values(g INT,v INT)", 0))
		run(referenceExec("INSERT INTO am_values VALUES(1,1),(1,1),(2,NULL),(2,2)", 4))
		aggregateModifiersBoth(run, referenceSelect("SELECT count(v),count(*),count(DISTINCT v),count(ALL v),count(v) FILTER(WHERE v=1),count(*) FILTER(WHERE v IS NULL),count(DISTINCT v) FILTER(WHERE v=1) FROM am_values", []string{"count", "count", "count", "count", "count", "count", "count"}, []string{"INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8"}, [][]any{{int64(3), int64(4), int64(2), int64(3), int64(2), int64(1), int64(1)}}))
		for _, where := range []string{" WHERE FALSE", " WHERE v IS NULL"} {
			aggregateModifiersBoth(run, referenceSelect("SELECT count(DISTINCT v),avg(DISTINCT v),min(ALL v),string_agg(v::text,',' ORDER BY v) FILTER(WHERE FALSE) FROM am_values"+where, []string{"count", "avg", "min", "string_agg"}, []string{"INT8", "NUMERIC", "INT4", "TEXT"}, [][]any{{int64(0), nil, nil, nil}}))
		}
		aggregateModifiersBoth(run, referenceSelect("SELECT g,count(v) FILTER(WHERE FALSE),avg(v) FILTER(WHERE NULL) FROM am_values GROUP BY g ORDER BY g", []string{"g", "count", "avg"}, []string{"INT4", "INT8", "NUMERIC"}, [][]any{{int64(1), int64(0), nil}, {int64(2), int64(0), nil}}))
		aggregateModifiersBoth(run, referenceSelect("SELECT g,count(DISTINCT v) FROM am_values WHERE FALSE GROUP BY g", []string{"g", "count"}, []string{"INT4", "INT8"}, nil))
	})
}

func TestReferenceAggregateModifiersParameters(t *testing.T) {
	aggregateModifiersScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep)) {
		run(referenceExec("CREATE TABLE am_params(v INT)", 0))
		run(referenceExec("INSERT INTO am_params VALUES(1),(1),(2)", 3))
		for _, tc := range []struct {
			q    string
			args []any
			want int64
		}{
			{"SELECT count(v) FILTER(WHERE $1) FROM am_params", []any{true}, 3},
			{"SELECT count(v) FILTER(WHERE $1) FROM am_params", []any{false}, 0},
			{"SELECT count(v) FILTER(WHERE $1) FROM am_params", []any{nil}, 0},
			{"SELECT count(DISTINCT $1) FROM am_params", []any{"x"}, 1},
			{"SELECT count(DISTINCT $1) FROM am_params", []any{nil}, 0},
			{"SELECT count($1 ORDER BY $1) FROM am_params", []any{"x"}, 3},
			{"SELECT count($1 ORDER BY $1) FROM am_params", []any{nil}, 0},
			{"SELECT count(DISTINCT 'x') FROM am_params", nil, 1},
			{"SELECT count(DISTINCT NULL) FROM am_params", nil, 0},
			{"SELECT count(v) FILTER(WHERE 'true') FROM am_params", nil, 3},
		} {
			aggregateModifiersBoth(run, referenceSelect(tc.q, []string{"count"}, []string{"INT8"}, [][]any{{tc.want}}, tc.args...))
		}
		orderedParameterQuery := "SELECT count($1 ORDER BY $1) FROM am_params"
		orderedParameterStmt, err := c.PrepareContext(p.ctx, orderedParameterQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer orderedParameterStmt.Close()
		for _, tc := range []struct {
			arg  any
			want int64
		}{{"x", 3}, {nil, 0}, {"y", 3}} {
			referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: c, statement: orderedParameterStmt, query: orderedParameterQuery}, b.recorder, referenceSelect(orderedParameterQuery, []string{"count"}, []string{"INT8"}, [][]any{{tc.want}}, tc.arg))
		}
		q := "SELECT count(DISTINCT v) FILTER(WHERE $1) FROM am_params"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range []struct {
			arg  any
			want int64
		}{{true, 2}, {false, 0}, {nil, 0}, {true, 2}} {
			referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}, b.recorder, referenceSelect(q, []string{"count"}, []string{"INT8"}, [][]any{{tc.want}}, tc.arg))
		}
		run(referenceExec("INSERT INTO am_params VALUES(3)", 1))
		referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}, b.recorder, referenceSelect(q, []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}, true))
		plainCount := aggregateModifiersError("SELECT count($1) FROM am_params", "42P18", "could not determine data type of parameter $1", "")
		plainCount.args = []any{"x"}
		aggregateModifiersBoth(run, plainCount)
	})
}

func TestReferenceAggregateModifiersTupleDistinct(t *testing.T) {
	aggregateModifiersScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep)) {
		run(referenceExec("CREATE TABLE am_tuple(id INT,v TEXT,d TEXT)", 0))
		run(referenceExec("INSERT INTO am_tuple VALUES(1,'a','/'),(2,'a','/'),(3,'a','!'),(4,NULL,'?'),(5,'a',NULL),(6,'a',NULL)", 6))
		aggregateModifiersBoth(run, referenceSelect("SELECT string_agg(DISTINCT v,d ORDER BY d NULLS LAST),string_agg(ALL v,d ORDER BY id),string_agg(v,d ORDER BY id) FILTER(WHERE id=3) FROM am_tuple", []string{"string_agg", "string_agg", "string_agg"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"a/aa", "a/a!aaa", "a"}}))
		run(referenceExec("CREATE TABLE am_tuple_bytes(v BYTEA,d BYTEA)", 0))
		run(referenceExec("INSERT INTO am_tuple_bytes VALUES('a','/'),('a','/'),('a','!'),(NULL,'?'),('a',NULL),('a',NULL)", 6))
		aggregateModifiersBoth(run, referenceSelect("SELECT string_agg(DISTINCT v,d ORDER BY d NULLS LAST) FROM am_tuple_bytes", []string{"string_agg"}, []string{"BYTEA"}, [][]any{{"a/aa"}}))
		run(referenceExec("CREATE TABLE am_binary(v BYTEA,d BYTEA)", 0))
		run(referenceExec("INSERT INTO am_binary VALUES('\\x00ff','\\x80'),('\\x00ff','\\x80'),('\\x00ff','\\x00'),('\\x00ff',NULL),(NULL,'\\xff')", 5))
		aggregateModifiersBoth(run, referenceSelect("SELECT string_agg(DISTINCT v,d ORDER BY d NULLS LAST) FROM am_binary", []string{"string_agg"}, []string{"BYTEA"}, [][]any{{string([]byte{0, 255, 128, 0, 255, 0, 255})}}))
	})
}
