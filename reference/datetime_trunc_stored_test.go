package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
)

func datetimeTruncStored(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
	t.Helper()
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
	run(referenceExec("CREATE DOMAIN dt_domain AS timestamp", 0))
	run(referenceExec("CREATE TABLE dt_domain_values(v dt_domain,CONSTRAINT dt_domain_day CHECK(date_trunc('day',v)=v))", 0))
	run(referenceExec("INSERT INTO dt_domain_values VALUES(TIMESTAMP '2001-02-16')", 1))
	datetimeExtractBoth(t, p, b, c, referenceSelect("SELECT date_trunc('month',v)::text AS result FROM dt_domain_values", []string{"result"}, []string{"TEXT"}, [][]any{{"2001-02-01 00:00:00"}}))
	run(referenceExec("CREATE TABLE dt_stored(id integer,s timestamp,g timestamp GENERATED ALWAYS AS (date_trunc('month',s)) STORED,d timestamp DEFAULT date_trunc('week',TIMESTAMP '2006-01-01'),z timestamptz DEFAULT date_trunc('day',TIMESTAMPTZ '2024-03-10 12:00+00'),i interval DEFAULT date_trunc('hour',TIME '24:00'),CONSTRAINT dt_check CHECK(date_trunc('year',s)>=TIMESTAMP '2000-01-01'))", 0))
	run(referenceExec("CREATE INDEX dt_index ON dt_stored ((date_trunc('month',s)))", 0))
	run(referenceExec("CREATE TABLE dt_explicit(t timestamptz,g timestamptz GENERATED ALWAYS AS (date_trunc('day',t,'America/New_York')) STORED,i interval,g_i interval GENERATED ALWAYS AS (date_trunc('hour',i)) STORED)", 0))
	run(referenceExec("CREATE INDEX dt_explicit_index ON dt_explicit ((date_trunc('day',t,'America/New_York')))", 0))
	run(referenceExec("CREATE TABLE dt_null(t timestamptz,g timestamptz GENERATED ALWAYS AS (date_trunc(NULL::text,t)) STORED)", 0))
	datetimeTruncExistingGenerated(t, p, b, c)
	for _, tc := range []struct{ q, message string }{
		{"CREATE TABLE dt_stable(t timestamptz,g timestamptz GENERATED ALWAYS AS (date_trunc('day',t)) STORED)", "generation expression is not immutable"},
		{"CREATE TABLE dt_date_stable(d date,g timestamptz GENERATED ALWAYS AS (date_trunc('day',d)) STORED)", "generation expression is not immutable"},
		{"CREATE INDEX dt_stable_index ON dt_stored ((date_trunc('day',z)))", "functions in index expression must be marked IMMUTABLE"},
	} {
		for _, prepared := range []bool{false, true} {
			s := referenceErrorDiagnostic(tc.q, controlsReferenceDiagnostic("42P17", tc.message))
			s.prepared, s.errorStage = prepared, "execute"
			run(s)
		}
	}
	run(referenceExec("INSERT INTO dt_stored(id,s) VALUES(1,TIMESTAMP '2001-02-16 12:34'),(2,TIMESTAMP '2001-03-16 12:34')", 2))
	datetimeTruncExistingChecks(t, p, b, c)
	run(referenceExec("CREATE VIEW dt_view AS SELECT id,date_trunc('quarter',s) AS q,date_trunc('hour',TIME '24:00') AS clock FROM dt_stored", 0))
	want := referenceSelect("SELECT id,q::text,clock::text FROM dt_view ORDER BY id", []string{"id", "q", "clock"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "2001-01-01 00:00:00", "24:00:00"}, {int64(2), "2001-01-01 00:00:00", "24:00:00"}})
	datetimeExtractBoth(t, p, b, c, want)
	for _, pretty := range []string{"false", "true"} {
		rows, err := c.QueryContext(p.ctx, "SELECT pg_get_viewdef('dt_view'::regclass,"+pretty+")")
		if err != nil {
			t.Fatal(err)
		}
		var definition string
		if !rows.Next() {
			rows.Close()
			t.Fatal("missing definition")
		}
		if err := rows.Scan(&definition); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
		run(referenceExec("CREATE VIEW dt_replay_"+pretty+" AS "+definition, 0))
		s := want
		s.query = "SELECT id,q::text,clock::text FROM dt_replay_" + pretty + " ORDER BY id"
		run(s)
	}
	held, err := c.PrepareContext(p.ctx, want.query)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	x := referenceHeldStatement{referenceExecutor: c, statement: held, query: want.query}
	run(referenceExec("ALTER TABLE dt_stored RENAME COLUMN s TO stamp", 0))
	// PostgreSQL rejects ALTER TYPE for a generated-column or view source.
	// A separate CHECK/index table exercises compatible descriptor remapping.
	run(referenceExec("CREATE TABLE dt_retag(v timestamp CHECK(date_trunc('day',v)=v))", 0))
	run(referenceExec("CREATE INDEX dt_retag_index ON dt_retag ((date_trunc('month',v)))", 0))
	run(referenceExec("INSERT INTO dt_retag VALUES(TIMESTAMP '2001-02-16')", 1))
	retagQuery := "SELECT date_trunc('month',v)::text AS result FROM dt_retag"
	retagStmt, err := c.PrepareContext(p.ctx, retagQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer retagStmt.Close()
	retagHeld := referenceHeldStatement{referenceExecutor: c, statement: retagStmt, query: retagQuery}
	retagWant := referenceSelect(retagQuery, []string{"result"}, []string{"TEXT"}, [][]any{{"2001-02-01 00:00:00"}})
	referenceRunStep(t, p.ctx, retagHeld, b.recorder, retagWant)
	run(referenceExec("ALTER TABLE dt_retag ALTER COLUMN v TYPE timestamp(6)", 0))
	referenceRunStep(t, p.ctx, retagHeld, b.recorder, retagWant)
	run(referenceExec("ALTER TABLE dt_stored RENAME TO dt_renamed", 0))
	run(referenceExec("CREATE OR REPLACE VIEW dt_view AS SELECT id,date_trunc('quarter',stamp) AS q,date_trunc('hour',TIME '24:00') AS clock FROM dt_renamed", 0))
	run(want)
	referenceRunStep(t, p.ctx, x, b.recorder, want)
	run(referenceExec("SET TIME ZONE 'America/New_York'", 0))
	run(referenceExec("INSERT INTO dt_renamed(id,stamp) VALUES(3,TIMESTAMP '2002-06-01')", 1))
	run(referenceSelect("SELECT g::text,d::text,z AT TIME ZONE 'UTC',i::text FROM dt_renamed WHERE id=3", []string{"g", "d", "timezone", "i"}, []string{"TEXT", "TEXT", "TIMESTAMP", "TEXT"}, [][]any{{"2002-06-01 00:00:00", "2005-12-26 00:00:00", datetimeTruncInstant("2024-03-10T05:00:00Z"), "24:00:00"}}))
	run(referenceExec("DELETE FROM dt_renamed WHERE id=3", 1))
	if b.pg {
		return
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	old, err := p.native.QueryContext(p.ctx, want.query)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	metadata, err := old.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	temporal, err := p.native.QueryContext(p.ctx, "SELECT date_trunc('month',stamp) AS bucket,date_trunc('hour',TIME '24:00') AS clock FROM dt_renamed ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer temporal.Close()
	temporalMetadata, err := temporal.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	destination, wrapped, record := timezoneDestination(t)
	if err := stuntgres.Restore(p.ctx, destination, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	referenceRunStep(t, p.ctx, wrapped, record, want)
	referenceRunStep(t, p.ctx, wrapped, record, retagWant)
	referenceRunStep(t, p.ctx, wrapped, record, referenceExec("INSERT INTO dt_renamed(id,stamp) VALUES(9,TIMESTAMP '2009-07-02')", 1))
	referenceRunStep(t, p.ctx, wrapped, record, referenceSelect("SELECT g::text,d::text,i::text FROM dt_renamed WHERE id=9", []string{"g", "d", "i"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"2009-07-01 00:00:00", "2005-12-26 00:00:00", "24:00:00"}}))
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	for _, wantRow := range want.rows {
		if !old.Next() {
			t.Fatalf("retained row: %v", old.Err())
		}
		var id int64
		var q, clock string
		if err := old.Scan(&id, &q, &clock); err != nil {
			t.Fatal(err)
		}
		if id != wantRow[0] || q != wantRow[1] || clock != wantRow[2] {
			t.Fatalf("retained row: %v %v %v", id, q, clock)
		}
	}
	if old.Next() || old.Err() != nil {
		t.Fatal("retained tail")
	}
	old.Close()
	if metadata[0].DatabaseTypeName() != "INT4" || metadata[1].DatabaseTypeName() != "TEXT" {
		t.Fatal("retained metadata")
	}
	for _, expected := range []string{"2001-02-01T00:00:00Z", "2001-03-01T00:00:00Z"} {
		if !temporal.Next() {
			t.Fatalf("retained temporal row: %v", temporal.Err())
		}
		var bucket, clock any
		if err := temporal.Scan(&bucket, &clock); err != nil {
			t.Fatal(err)
		}
		if bucket != datetimeTruncInstant(expected) || clock != "24:00:00" {
			t.Fatalf("retained raw temporal row: %T %v, %T %v", bucket, bucket, clock, clock)
		}
	}
	if temporal.Next() || temporal.Err() != nil {
		t.Fatal("retained temporal tail")
	}
	temporal.Close()
	if temporalMetadata[0].Name() != "bucket" || temporalMetadata[0].DatabaseTypeName() != "TIMESTAMP" || temporalMetadata[1].Name() != "clock" || temporalMetadata[1].DatabaseTypeName() != "INTERVAL" {
		t.Fatal("retained temporal metadata")
	}
	bad := dump.String() + "\nCREATE TABLE dt_bad(v timestamp DEFAULT date_trunc('bad',TIMESTAMP '2001-01-01')); INSERT INTO dt_bad DEFAULT VALUES;"
	d := datetimeExtractFieldDiagnostic("22023", "bad", "timestamp without time zone")
	referenceAssertDiagnostic(t, stuntgres.Restore(p.ctx, p.native, strings.NewReader(bad)), &d)
	run(want)
	referenceRunStep(t, p.ctx, x, b.recorder, want)
	ctx, cancel := context.WithCancel(p.ctx)
	cancel()
	if err := stuntgres.Restore(ctx, p.native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled restore: %v", err)
	}
	failure := errors.New("trunc restore reader failure")
	if err := stuntgres.Restore(p.ctx, p.native, timezoneFailingReader{failure}); !errors.Is(err, failure) {
		t.Fatalf("reader error: %v", err)
	}
	run(want)
	referenceRunStep(t, p.ctx, x, b.recorder, want)
	if p.postgres != nil {
		if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
			t.Fatalf("actual native replay: %v\n%s", err, dump.String())
		}
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, retagWant)
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, want)
		if _, err := p.postgres.ExecContext(p.ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferenceDatetimeTruncStoredDefinitionsReplay(t *testing.T) {
	datetimeTruncScenario(t, datetimeTruncStored)
}

func TestReferenceDatetimeTruncFeatureSmoke(t *testing.T) {
	t.Run("boundaries", func(t *testing.T) {
		datetimeTruncScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
			datetimeTruncValue(t, p, b, c, "date_trunc('week',TIMESTAMP '2006-01-01')::text", "TEXT", "2005-12-26 00:00:00")
			datetimeTruncValue(t, p, b, c, "date_trunc('quarter',TIMESTAMP '2001-02-16 20:38:40')::text", "TEXT", "2001-01-01 00:00:00")
			datetimeTruncValue(t, p, b, c, "date_trunc('second',INTERVAL '-00:00:01.999999')::text", "TEXT", "-00:00:01")
			datetimeTruncValue(t, p, b, c, "date_trunc('hour',TIME '24:00')", "INTERVAL", "24:00:00")
			datetimeTruncValue(t, p, b, c, "date_trunc('day',DATE '2001-02-16')", "TIMESTAMPTZ", "2001-02-16T00:00:00Z")
		})
	})
	t.Run("zones", func(t *testing.T) { datetimeTruncScenario(t, datetimeTruncZones) })
	t.Run("held", func(t *testing.T) { datetimeTruncScenario(t, datetimeTruncHeld) })
	t.Run("consumers", func(t *testing.T) { datetimeTruncScenario(t, datetimeTruncConsumers) })
	t.Run("stored", func(t *testing.T) { datetimeTruncScenario(t, datetimeTruncStored) })
}
