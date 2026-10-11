package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceExtremaCallers(t *testing.T) {
	extremaReference(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
		run(fixture.Exec("CREATE TABLE extrema_calls(k TEXT,v INTEGER)", 0))
		run(fixture.Exec("INSERT INTO extrema_calls VALUES('a',3),('a',-2),('b',8),('b',8),(NULL,NULL)", 5))
		run(fixture.Exec("CREATE TABLE extrema_labels(k TEXT)", 0))
		run(fixture.Exec("INSERT INTO extrema_labels VALUES('a'),('b')", 2))
		for _, s := range []fixture.Step{
			fixture.Select("SELECT min(v),max(v),min(v),count(v),avg(v) FROM extrema_calls", []string{"min", "max", "min", "count", "avg"}, []string{"INT4", "INT4", "INT4", "INT8", "NUMERIC"}, [][]any{{int64(-2), int64(8), int64(-2), int64(4), "4.2500000000000000"}}),
			fixture.Select("SELECT min(v)+1 AS lo,max(v)-1 AS hi FROM extrema_calls", []string{"lo", "hi"}, []string{"INT4", "INT4"}, [][]any{{int64(-1), int64(7)}}),
			fixture.Select("SELECT c.k AS label,min(c.v) AS lo,max(c.v) AS hi FROM extrema_calls AS c JOIN extrema_labels AS l ON c.k=l.k GROUP BY c.k ORDER BY max(c.v)", []string{"label", "lo", "hi"}, []string{"TEXT", "INT4", "INT4"}, [][]any{{"a", int64(-2), int64(3)}, {"b", int64(8), int64(8)}}),
			fixture.Select("SELECT v+1 AS key,min(v),max(v) FROM extrema_calls GROUP BY v+1 ORDER BY key", []string{"key", "min", "max"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(-1), int64(-2), int64(-2)}, {int64(4), int64(3), int64(3)}, {int64(9), int64(8), int64(8)}, {nil, nil, nil}}),
			fixture.Select("SELECT min(v),max(v) FROM extrema_calls HAVING min(v)>10", []string{"min", "max"}, []string{"INT4", "INT4"}, nil),
			fixture.Select("SELECT pg_catalog.min(v),\"max\"(v) FROM extrema_calls", []string{"min", "max"}, []string{"INT4", "INT4"}, [][]any{{int64(-2), int64(8)}}),
		} {
			extremaBoth(t, p, conn, recorder, s)
		}
	})
}

func TestReferenceExtremaInputOrders(t *testing.T) {
	extremaReference(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
		run(fixture.Exec("CREATE TABLE extrema_order(v BIGINT)", 0))
		for i, values := range []string{
			"(-9223372036854775808),(-9007199254740993),(NULL),(9007199254740993),(9223372036854775807)",
			"(9223372036854775807),(9007199254740993),(NULL),(-9007199254740993),(-9223372036854775808)",
			"(9007199254740993),(-9223372036854775808),(9223372036854775807),(NULL),(-9007199254740993)",
		} {
			if i != 0 {
				run(fixture.Exec("DELETE FROM extrema_order", 5))
			}
			run(fixture.Exec("INSERT INTO extrema_order VALUES"+values, 5))
			extremaBoth(t, p, conn, recorder, fixture.Select("SELECT min(v),max(v) FROM extrema_order", []string{"min", "max"}, []string{"INT8", "INT8"}, [][]any{{int64(-9223372036854775808), int64(9223372036854775807)}}))
		}
	})
}

func TestReferenceExtremaVolatileEvaluation(t *testing.T) {
	extremaReference(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
		run(fixture.Exec("CREATE TABLE extrema_volatile(v INT)", 0))
		run(fixture.Exec("INSERT INTO extrema_volatile VALUES(1),(2),(3)", 3))
		run(fixture.Exec("CREATE SEQUENCE extrema_seq", 0))
		// Each argument occurrence runs for every row, despite a retained winner;
		// duplicate volatile min calls must not be deduplicated.
		run(fixture.Select("SELECT min(nextval('extrema_seq')),min(nextval('extrema_seq')),max(nextval('extrema_seq')) FROM extrema_volatile", []string{"min", "min", "max"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(1), int64(2), int64(9)}}))
		run(fixture.Select("SELECT currval('extrema_seq')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(9)}}))
	})
}
