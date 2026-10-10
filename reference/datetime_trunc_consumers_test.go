package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func datetimeTruncConsumers(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
	t.Helper()
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
	run(referenceExec("CREATE TABLE dt_rows(id integer,f text,s timestamp,z text,t timestamptz)", 0))
	run(referenceExec("INSERT INTO dt_rows VALUES(1,'day',TIMESTAMP '2001-02-16 12:00','UTC',TIMESTAMPTZ '2001-02-16 12:00+00'),(2,'bad',TIMESTAMP '2001-02-16 13:00','Not/AZone',TIMESTAMPTZ '2001-02-16 13:00+00')", 2))
	run(referenceExec("CREATE TABLE dt_target(id integer,v timestamp)", 0))
	run(referenceExec("INSERT INTO dt_target VALUES(9,TIMESTAMP '1999-01-01')", 1))
	for _, q := range []string{"INSERT INTO dt_target SELECT id,date_trunc(f,s) FROM dt_rows ORDER BY id", "UPDATE dt_target SET v=(SELECT date_trunc(f,s) FROM dt_rows WHERE id=2)"} {
		for _, prepared := range []bool{false, true} {
			s := referenceErrorDiagnostic(q, datetimeExtractFieldDiagnostic("22023", "bad", "timestamp without time zone"))
			s.prepared, s.errorStage = prepared, "execute"
			run(s)
			run(referenceSelect("SELECT id,v::text FROM dt_target", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(9), "1999-01-01 00:00:00"}}))
		}
	}
	run(referenceExec("CREATE TABLE dt_zone_target(v timestamptz)", 0))
	s := referenceErrorDiagnostic("INSERT INTO dt_zone_target SELECT date_trunc('day',t,z) FROM dt_rows ORDER BY id", controlsReferenceDiagnostic("22023", `time zone "Not/AZone" not recognized`))
	s.errorStage = "execute"
	run(s)
	run(referenceSelect("SELECT count(*) FROM dt_zone_target", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	run(referenceExec("UPDATE dt_rows SET f='day',z='UTC' WHERE id=2", 1))
	for _, tc := range []referenceStep{
		referenceSelect("SELECT date_trunc('day',s)::text AS bucket,count(*) AS n FROM dt_rows GROUP BY date_trunc('day',s) ORDER BY date_trunc('day',s)", []string{"bucket", "n"}, []string{"TEXT", "INT8"}, [][]any{{"2001-02-16 00:00:00", int64(2)}}),
		referenceSelect("SELECT count(date_trunc('hour',s)) AS n,min(date_trunc('hour',s))::text AS first FROM dt_rows", []string{"n", "first"}, []string{"INT8", "TEXT"}, [][]any{{int64(2), "2001-02-16 12:00:00"}}),
		referenceSelect("SELECT a.id,b.id FROM dt_rows a JOIN dt_rows b ON date_trunc('hour',a.s)=date_trunc('hour',b.s) ORDER BY a.id", []string{"id", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}}),
		referenceSelect("SELECT CASE WHEN id=1 THEN date_trunc('day',s) ELSE COALESCE(NULL::timestamp,date_trunc('day',s)) END::text AS v FROM dt_rows WHERE date_trunc('day',s)=TIMESTAMP '2001-02-16' ORDER BY id", []string{"v"}, []string{"TEXT"}, [][]any{{"2001-02-16 00:00:00"}, {"2001-02-16 00:00:00"}}),
	} {
		datetimeExtractBoth(t, p, b, c, tc)
	}
	run(referenceExec("INSERT INTO dt_target VALUES(3,date_trunc('month',TIMESTAMP '2003-04-05 12:34'))", 1))
	run(referenceSelect("SELECT date_trunc('month',v)::text AS v FROM dt_target WHERE id=3", []string{"v"}, []string{"TEXT"}, [][]any{{"2003-04-01 00:00:00"}}))
	run(referenceSelect("UPDATE dt_target SET v=TIMESTAMP '2004-04-05' WHERE id=3 RETURNING date_trunc('year',v)::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"2004-01-01 00:00:00"}}))
	run(referenceSelect("DELETE FROM dt_target WHERE id=3 RETURNING date_trunc('quarter',v)::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"2004-04-01 00:00:00"}}))
	run(referenceExec("BEGIN", 0))
	run(referenceExec("SAVEPOINT dt_save", 0))
	s = referenceErrorDiagnostic("INSERT INTO dt_target VALUES(3,date_trunc('bad',TIMESTAMP '2001-01-01'))", datetimeExtractFieldDiagnostic("22023", "bad", "timestamp without time zone"))
	s.errorStage = "execute"
	run(s)
	run(referenceErrorDiagnostic("SELECT count(*) FROM dt_target", controlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
	run(referenceExec("ROLLBACK TO SAVEPOINT dt_save", 0))
	run(referenceExec("COMMIT", 0))
	run(referenceSelect("SELECT count(*) FROM dt_target", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
	if b.pg {
		// pq may complete a fast INSERT despite an already canceled Go context.
		// Observe server admission before cancellation, using the shared fixture.
		for _, prepared := range []bool{false, true} {
			referenceSessionCancel(t, p.ctx, c, prepared)
		}
	} else {
		ctx, cancel := context.WithCancel(p.ctx)
		cancel()
		if _, err := c.ExecContext(ctx, "INSERT INTO dt_target VALUES(4,date_trunc('day',TIMESTAMP '2001-01-01'))"); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled insert: %v", err)
		}
	}
	run(referenceSelect("SELECT count(*) FROM dt_target", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
}

func TestReferenceDatetimeTruncConsumersAtomicity(t *testing.T) {
	datetimeTruncScenario(t, datetimeTruncConsumers)
}
