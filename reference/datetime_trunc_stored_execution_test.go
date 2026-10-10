package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
)

func datetimeTruncExistingGenerated(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
	t.Helper()
	referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("INSERT INTO dt_explicit(t,i) VALUES(TIMESTAMPTZ '2024-03-10 12:00+00',INTERVAL '49 hours 5 minutes')", 1))
	datetimeExtractBoth(t, p, b, c, referenceSelect("SELECT g AT TIME ZONE 'UTC',g_i::text FROM dt_explicit", []string{"timezone", "g_i"}, []string{"TIMESTAMP", "TEXT"}, [][]any{{datetimeTruncInstant("2024-03-10T05:00:00Z"), "49:00:00"}}))
	referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("INSERT INTO dt_null(t) VALUES(TIMESTAMPTZ 'infinity')", 1))
	datetimeExtractBoth(t, p, b, c, referenceSelect("SELECT g FROM dt_null", []string{"g"}, []string{"TIMESTAMPTZ"}, [][]any{{nil}}))
}

func datetimeTruncExistingChecks(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
	t.Helper()
	for _, tc := range []struct {
		table, constraint, query, detail string
		count                            int64
	}{
		{"dt_domain_values", "dt_domain_day", "INSERT INTO dt_domain_values VALUES(TIMESTAMP '2002-01-01'),(TIMESTAMP '2001-02-16 12:00')", "Failing row contains (2001-02-16 12:00:00).", 1},
		{"dt_stored", "dt_check", "INSERT INTO dt_stored(id,s) VALUES(3,TIMESTAMP '2002-01-01'),(4,TIMESTAMP '1999-01-01')", "Failing row contains (4, 1999-01-01 00:00:00, 1999-01-01 00:00:00, 2005-12-26 00:00:00, 2024-03-10 00:00:00+00, 24:00:00).", 2},
	} {
		d := controlsReferenceDiagnostic("23514", `new row for relation "`+tc.table+`" violates check constraint "`+tc.constraint+`"`)
		d.Detail, d.Schema, d.Table, d.Constraint = tc.detail, "public", tc.table, tc.constraint
		for _, prepared := range []bool{false, true} {
			s := referenceErrorDiagnostic(tc.query, d)
			s.prepared, s.errorStage = prepared, "execute"
			referenceRunStep(t, p.ctx, c, b.recorder, s)
			referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT count(*) FROM "+tc.table, []string{"count"}, []string{"INT8"}, [][]any{{tc.count}}))
		}
	}
}

func datetimeTruncCheckWrite(t *testing.T, p *referencePair, pg bool, x referenceExecutor, record *referenceRecorder) {
	t.Helper()
	d := controlsReferenceDiagnostic("23514", `new row for relation "dr_check" violates check constraint "dr_midnight"`)
	d.Detail = "Failing row contains (2001-02-16 12:00:00)."
	d.Schema, d.Table, d.Constraint = "public", "dr_check", "dr_midnight"
	for _, prepared := range []bool{false, true} {
		s := referenceErrorDiagnostic("INSERT INTO dr_check VALUES(TIMESTAMP '2002-01-01'),(TIMESTAMP '2001-02-16 12:00')", d)
		s.prepared, s.errorStage = prepared, "execute"
		referenceRunStep(t, p.ctx, x, record, s)
		referenceRunStep(t, p.ctx, x, record, referenceSelect("SELECT v::text FROM dr_check", []string{"v"}, []string{"TEXT"}, [][]any{{"2001-02-16 00:00:00"}}))
	}
}

func datetimeTruncStoredInsert(t *testing.T, p *referencePair, x referenceExecutor, record *referenceRecorder, id int64) {
	t.Helper()
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, x, record, s) }
	run(referenceExec("SET TIME ZONE 'America/New_York'", 0))
	run(referenceExec("INSERT INTO dr_defaults(id,s) VALUES($1,TIMESTAMP '2009-07-02 12:34')", 1, id))
	run(referenceSelect("SELECT g::text,d::text,z AT TIME ZONE 'UTC',i::text FROM dr_defaults WHERE id=$1", []string{"g", "d", "timezone", "i"}, []string{"TEXT", "TEXT", "TIMESTAMP", "TEXT"}, [][]any{{"2009-07-01 00:00:00", "2005-12-26 00:00:00", datetimeTruncInstant("2024-03-10T05:00:00Z"), "24:00:00"}}, id))
	run(referenceExec("INSERT INTO dr_explicit(id,t,i) VALUES($1,TIMESTAMPTZ '2024-03-10 12:00+00',INTERVAL '49 hours 5 minutes')", 1, id))
	run(referenceSelect("SELECT g AT TIME ZONE 'UTC',g_i::text FROM dr_explicit WHERE id=$1", []string{"timezone", "g_i"}, []string{"TIMESTAMP", "TEXT"}, [][]any{{datetimeTruncInstant("2024-03-10T05:00:00Z"), "49:00:00"}}, id))
	run(referenceExec("INSERT INTO dr_null(id,t) VALUES($1,TIMESTAMPTZ 'infinity')", 1, id))
	run(referenceSelect("SELECT g FROM dr_null WHERE id=$1", []string{"g"}, []string{"TIMESTAMPTZ"}, [][]any{{nil}}, id))
}

func datetimeTruncStoredExecution(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
	t.Helper()
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
	run(referenceExec("CREATE TABLE dr_defaults(id integer,s timestamp,g timestamp GENERATED ALWAYS AS (date_trunc('month',s)) STORED,d timestamp DEFAULT date_trunc('week',TIMESTAMP '2006-01-01'),z timestamptz DEFAULT date_trunc('day',TIMESTAMPTZ '2024-03-10 12:00+00'),i interval DEFAULT date_trunc('hour',TIME '24:00'))", 0))
	run(referenceExec("CREATE TABLE dr_explicit(id integer,t timestamptz,g timestamptz GENERATED ALWAYS AS (date_trunc('day',t,'America/New_York')) STORED,i interval,g_i interval GENERATED ALWAYS AS (date_trunc('hour',i)) STORED)", 0))
	run(referenceExec("CREATE INDEX dr_explicit_index ON dr_explicit ((date_trunc('day',t,'America/New_York')))", 0))
	run(referenceExec("CREATE TABLE dr_null(id integer,t timestamptz,g timestamptz GENERATED ALWAYS AS (date_trunc(NULL::text,t)) STORED)", 0))
	run(referenceExec("CREATE TABLE dr_check(v timestamp,CONSTRAINT dr_midnight CHECK(date_trunc('day',v)=v))", 0))
	run(referenceExec("INSERT INTO dr_check VALUES(TIMESTAMP '2001-02-16')", 1))
	datetimeTruncStoredInsert(t, p, c, b.recorder, 1)
	datetimeTruncCheckWrite(t, p, b.pg, c, b.recorder)
	if b.pg {
		return
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	destination, wrapped, record := timezoneDestination(t)
	if err := stuntgres.Restore(p.ctx, destination, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	dest, err := wrapped.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer dest.Close()
	datetimeTruncStoredInsert(t, p, dest, record, 9)
	datetimeTruncCheckWrite(t, p, false, dest, record)
	if p.postgres != nil {
		pg, err := p.postgres.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer pg.Close()
		if _, err := pg.ExecContext(p.ctx, dump.String()); err != nil {
			t.Fatalf("actual native dump replay: %v\n%s", err, dump.String())
		}
		datetimeTruncStoredInsert(t, p, pg, p.postgresRecord, 9)
		datetimeTruncCheckWrite(t, p, true, pg, p.postgresRecord)
		if _, err := pg.ExecContext(p.ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferenceDatetimeTruncStoredExecutionReplay(t *testing.T) {
	datetimeTruncScenario(t, datetimeTruncStoredExecution)
}
