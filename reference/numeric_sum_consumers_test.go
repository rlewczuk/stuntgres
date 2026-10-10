package reference_test

import (
	"database/sql"
	"testing"
)

func numericSumAtomicity(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder) {
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, recorder, s) }
	for _, c := range []struct{ name, kind, typ, bad, message string }{
		{"exact", "NUMERIC", "NUMERIC", "'9e131071'", "value overflows numeric format"},
		{"real", "REAL", "FLOAT4", "3.4028235e38", "value out of range: overflow"},
		{"double", "DOUBLE PRECISION", "FLOAT8", "1.7976931348623157e308", "value out of range: overflow"},
	} {
		source, target := "bad_"+c.name, "target_"+c.name
		run(referenceExec("CREATE TABLE "+source+"(g INTEGER,id INTEGER,v "+c.kind+")", 0))
		run(referenceExec("INSERT INTO "+source+" VALUES (1,1,1),(1,2,3),(2,3,"+c.bad+"),(2,4,"+c.bad+")", 4))
		run(referenceExec("CREATE TABLE "+target+"(g INTEGER,v "+c.kind+")", 0))
		run(referenceExec("INSERT INTO "+target+" VALUES (99,9)", 1))
		// Sorting by the aggregate result requires every group to finalize before
		// PostgreSQL can publish a cursor. Ordering only by g can stream the safe
		// first group and defer the later overflow until Rows.Next.
		q := "SELECT g,sum(v ORDER BY id) FROM " + source + " GROUP BY g ORDER BY sum(v ORDER BY id),g"
		numericSumExecutionError(t, p, conn, recorder, q, c.message, true)
		numericSumExecutionError(t, p, conn, recorder, "INSERT INTO "+target+" "+q, c.message, false)
		var want any = "9"
		if c.typ != "NUMERIC" {
			want = float64(9)
		}
		run(referenceSelect("SELECT g,v FROM "+target, []string{"g", "v"}, []string{"INT4", c.typ}, [][]any{{int64(99), want}}))
	}
	// Assignment fails after successful provider finalization; no prefix insert.
	run(referenceExec("CREATE TABLE assignment_source(g INTEGER,v INTEGER)", 0))
	run(referenceExec("INSERT INTO assignment_source VALUES (1,1),(2,32767),(2,1)", 3))
	run(referenceExec("CREATE TABLE assignment_target(g INTEGER,v SMALLINT)", 0))
	run(referenceExec("INSERT INTO assignment_target VALUES (99,9)", 1))
	numericSumExecutionError(t, p, conn, recorder, "INSERT INTO assignment_target SELECT g,sum(v) FROM assignment_source GROUP BY g ORDER BY g", "smallint out of range", false)
	run(referenceSelect("SELECT g,v FROM assignment_target", []string{"g", "v"}, []string{"INT4", "INT2"}, [][]any{{int64(99), int64(9)}}))
}

func TestReferenceNumericSumConsumers(t *testing.T) {
	numericSumScenario(t, func(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder, pg bool) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, recorder, s) }
		run(referenceExec("CREATE TABLE t(g INTEGER,v INTEGER)", 0))
		run(referenceExec("INSERT INTO t VALUES (1,1),(1,1),(1,2),(1,NULL),(2,NULL)", 5))
		numericSumBoth(t, p, conn, recorder, referenceSelect("SELECT sum(ALL v),sum(DISTINCT v),sum(v) FILTER(WHERE v>1),sum(v) FILTER(WHERE FALSE) FROM t", []string{"sum", "sum", "sum", "sum"}, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{int64(4), int64(3), int64(2), nil}}))
		numericSumBoth(t, p, conn, recorder, referenceSelect("SELECT sum(v),sum(v),avg(v),count(v) FROM t", []string{"sum", "sum", "avg", "count"}, []string{"INT8", "INT8", "NUMERIC", "INT8"}, [][]any{{int64(4), int64(4), "1.3333333333333333", int64(3)}}))
		numericSumBoth(t, p, conn, recorder, referenceSelect("SELECT g FROM t GROUP BY g HAVING sum(v)>2 ORDER BY sum(v)", []string{"g"}, []string{"INT4"}, [][]any{{int64(1)}}))
		numericSumBoth(t, p, conn, recorder, referenceSelect("SELECT sum(v)+1 AS total FROM t", []string{"total"}, []string{"INT8"}, [][]any{{int64(5)}}))
		numericSumBoth(t, p, conn, recorder, referenceSelect("SELECT q.total FROM (SELECT sum(v) AS total FROM t) AS q", []string{"total"}, []string{"INT8"}, [][]any{{int64(4)}}))
		numericSumBoth(t, p, conn, recorder, referenceSelect("SELECT (SELECT sum(v) FROM t) AS total", []string{"total"}, []string{"INT8"}, [][]any{{int64(4)}}))
		run(referenceExec("CREATE TABLE keys(g INTEGER)", 0))
		run(referenceExec("INSERT INTO keys VALUES (1),(2),(1)", 3))
		s := referenceSelect("SELECT k.g,(SELECT sum(t.v) FROM t WHERE t.g=k.g) AS total FROM keys AS k", []string{"g", "total"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(4)}, {int64(2), nil}, {int64(1), int64(4)}})
		s.bag = true
		numericSumBoth(t, p, conn, recorder, s)
		run(referenceExec("CREATE TABLE destination(g INTEGER,v BIGINT)", 0))
		for _, prepared := range []bool{false, true} {
			s = referenceExec("INSERT INTO destination SELECT g,sum(v) FROM t GROUP BY g", 2)
			s.prepared = prepared
			run(s)
			s = referenceExec("INSERT INTO destination(v) SELECT sum(v) FROM t WHERE FALSE", 1)
			s.prepared = prepared
			run(s)
			s = referenceExec("INSERT INTO destination SELECT g,sum(v) FROM t WHERE FALSE GROUP BY g", 0)
			s.prepared = prepared
			run(s)
		}
		s = referenceSelect("SELECT g,v FROM destination", []string{"g", "v"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(4)}, {int64(1), int64(4)}, {int64(2), nil}, {int64(2), nil}, {nil, nil}, {nil, nil}})
		s.bag = true
		run(s)
		// FILTER runs before argument evaluation, not after a failed cast.
		run(referenceExec("CREATE TABLE filtered(v TEXT,keep BOOLEAN)", 0))
		run(referenceExec("INSERT INTO filtered VALUES ('oops',FALSE),('4',TRUE)", 2))
		numericSumBoth(t, p, conn, recorder, referenceSelect("SELECT sum(v::integer) FILTER(WHERE keep) FROM filtered", []string{"sum"}, []string{"INT8"}, [][]any{{int64(4)}}))
		numericSumAtomicity(t, p, conn, recorder)
	})
}
