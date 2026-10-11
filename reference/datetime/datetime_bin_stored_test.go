package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func datetimeBinStoredInsert(t *testing.T, p *fixture.Pair, x fixture.Executor, record *fixture.Recorder, id int64) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, x, record, s) }
	run(fixture.Exec("SET TIME ZONE 'America/New_York'", 0))
	run(fixture.Exec("INSERT INTO db_stored(id,s,z) VALUES($1,TIMESTAMP '2000-01-01 00:17:29.999999',TIMESTAMPTZ '2000-01-01 00:17:30+00')", 1, id))
	run(fixture.Select("SELECT g,h,d,stable FROM db_stored WHERE id=$1", []string{"g", "h", "d", "stable"}, []string{"TIMESTAMP", "TIMESTAMPTZ", "TIMESTAMP", "TIMESTAMPTZ"}, [][]any{{datetimeTruncInstant("2000-01-01T00:02:30Z"), datetimeTruncInstant("2000-01-01T00:17:30Z"), datetimeTruncInstant("2000-01-01T00:02:30Z"), datetimeTruncInstant("2000-01-02T05:00:00Z")}}, id))
	run(fixture.Prepared(fixture.Exec("INSERT INTO db_zdefault(id) VALUES($1)", 1, id)))
	run(fixture.Select("SELECT t FROM db_zdefault WHERE id=$1", []string{"t"}, []string{"TIMESTAMPTZ"}, [][]any{{datetimeTruncInstant("2000-01-01T00:02:30Z")}}, id))
}

func datetimeBinCheckWrite(t *testing.T, p *fixture.Pair, x fixture.Executor, record *fixture.Recorder) {
	t.Helper()
	d := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "db_check" violates check constraint "db_aligned"`)
	d.Detail, d.Schema, d.Table, d.Constraint = "Failing row contains (2000-01-02 12:00:00).", "public", "db_check", "db_aligned"
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic("INSERT INTO db_check VALUES(TIMESTAMP '2000-01-03'),(TIMESTAMP '2000-01-02 12:00')", d)
		s.Prepared, s.ErrorStage = prepared, "execute"
		fixture.RunStep(t, p.Ctx, x, record, s)
		fixture.RunStep(t, p.Ctx, x, record, fixture.Select("SELECT v::text FROM db_check ORDER BY v", []string{"v"}, []string{"TEXT"}, [][]any{{"2000-01-01 00:00:00"}}))
	}
}

func datetimeBinStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE db_stored(id integer,s timestamp,z timestamptz,g timestamp GENERATED ALWAYS AS (pg_catalog.date_bin('15 minutes',s,TIMESTAMP '2000-01-01 00:02:30')) STORED,h timestamptz GENERATED ALWAYS AS (date_bin('15 minutes',z,TIMESTAMPTZ '2000-01-01 00:02:30+00')) STORED,d timestamp DEFAULT date_bin('15 minutes',TIMESTAMP '2000-01-01 00:07',TIMESTAMP '2000-01-01 00:02:30'),stable timestamptz DEFAULT date_bin('1 day',DATE '2000-01-02',DATE '2000-01-01'))", 0))
	run(fixture.Exec("CREATE INDEX db_naive_index ON db_stored ((date_bin('15 minutes',s,TIMESTAMP '2000-01-01 00:02:30')))", 0))
	run(fixture.Prepared(fixture.Exec("CREATE INDEX db_zoned_index ON db_stored ((date_bin('15 minutes',z,TIMESTAMPTZ '2000-01-01 00:02:30+00'))) WHERE z IS NOT NULL", 0)))
	run(fixture.Exec("CREATE TABLE db_check(v timestamp,CONSTRAINT db_aligned CHECK(date_bin('1 day',v,TIMESTAMP '2000-01-01')=v))", 0))
	run(fixture.Exec("INSERT INTO db_check VALUES(TIMESTAMP '2000-01-01')", 1))
	for _, tc := range []struct{ q, message string }{
		{"CREATE TABLE db_stable_generated(s timestamp,z timestamptz,g timestamptz GENERATED ALWAYS AS (date_bin('1 day',s,z)) STORED)", "generation expression is not immutable"},
		{"CREATE TABLE db_date_generated(s date,g timestamptz GENERATED ALWAYS AS (date_bin('1 day',s,DATE '2000-01-01')) STORED)", "generation expression is not immutable"},
		{"CREATE INDEX db_stable_index ON db_stored ((date_bin('1 day',s,z)))", "functions in index expression must be marked IMMUTABLE"},
	} {
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(tc.q, fixture.ControlsReferenceDiagnostic("42P17", tc.message))
			s.Prepared, s.ErrorStage = prepared, "execute"
			run(s)
		}
	}
	run(fixture.Exec("CREATE TABLE db_zdefault(id integer,t timestamptz DEFAULT date_bin('15 minutes',TIMESTAMPTZ '2000-01-01 00:07+00',TIMESTAMPTZ '2000-01-01 00:02:30+00'),CONSTRAINT db_zaligned CHECK(date_bin('15 minutes',t,TIMESTAMPTZ '2000-01-01 00:02:30+00')=t))", 0))
	datetimeBinStoredInsert(t, p, c, b.Recorder, 1)
	datetimeBinCheckWrite(t, p, c, b.Recorder)
	run(fixture.Exec("CREATE VIEW db_view AS SELECT id,pg_catalog.date_bin('15 minutes',db_stored.s,TIMESTAMP '2000-01-01 00:02:30') AS bucket,date_bin('15 minutes',db_stored.z,TIMESTAMPTZ '2000-01-01 00:02:30+00') AS instant FROM db_stored", 0))
	want := fixture.Select("SELECT id,bucket,instant FROM db_view ORDER BY id", []string{"id", "bucket", "instant"}, []string{"INT4", "TIMESTAMP", "TIMESTAMPTZ"}, [][]any{{int64(1), datetimeTruncInstant("2000-01-01T00:02:30Z"), datetimeTruncInstant("2000-01-01T00:17:30Z")}})
	datetimeExtractBoth(t, p, b, c, want)
	run(fixture.Exec("CREATE VIEW db_stable_view AS SELECT id,date_bin('15 minutes',s,z) AS mixed FROM db_stored", 0))
	for _, zone := range []struct{ name, want string }{{"UTC", "2000-01-01T00:02:30Z"}, {"America/New_York", "2000-01-01T05:02:30Z"}} {
		run(fixture.Exec("SET TIME ZONE '"+zone.name+"'", 0))
		datetimeExtractBoth(t, p, b, c, fixture.Select("SELECT mixed FROM db_stable_view ORDER BY id", []string{"mixed"}, []string{"TIMESTAMPTZ"}, [][]any{{datetimeTruncInstant(zone.want)}}))
	}
	for _, pretty := range []string{"false", "true"} {
		var definition string
		if err := c.QueryRowContext(p.Ctx, "SELECT pg_get_viewdef('db_view'::regclass,"+pretty+")").Scan(&definition); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("CREATE VIEW db_view_replay_"+pretty+" AS "+definition, 0))
		s := want
		s.Query = "SELECT id,bucket,instant FROM db_view_replay_" + pretty + " ORDER BY id"
		run(s)
	}
	run(fixture.Exec("ALTER TABLE db_stored RENAME COLUMN s TO stamp", 0))
	run(fixture.Exec("ALTER TABLE db_stored RENAME TO db_renamed", 0))
	datetimeExtractBoth(t, p, b, c, want)
	run(fixture.Exec("CREATE TABLE db_retag(v timestamp CHECK(date_bin('1 day',v,TIMESTAMP '2000-01-01')=v))", 0))
	run(fixture.Exec("CREATE INDEX db_retag_index ON db_retag ((date_bin('1 day',v,TIMESTAMP '2000-01-01')))", 0))
	run(fixture.Exec("INSERT INTO db_retag VALUES(TIMESTAMP '2000-01-02')", 1))
	retagQ := "SELECT date_bin('1 day',v,TIMESTAMP '2000-01-01') AS result FROM db_retag"
	stmt, err := c.PrepareContext(p.Ctx, retagQ)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: retagQ}
	retagWant := fixture.Select(retagQ, []string{"result"}, []string{"TIMESTAMP"}, [][]any{{datetimeTruncInstant("2000-01-02T00:00:00Z")}})
	fixture.RunStep(t, p.Ctx, held, b.Recorder, retagWant)
	run(fixture.Exec("ALTER TABLE db_retag ALTER COLUMN v TYPE timestamp(6)", 0))
	fixture.RunStep(t, p.Ctx, held, b.Recorder, retagWant)
	if b.PG {
		return
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	run(want)
	run(retagWant)
	// Invoke restored definitions using a fresh physical session. Rename back only
	// for the shared insert helper; the view/generated/index trees must remap.
	run(fixture.Exec("ALTER TABLE db_renamed RENAME TO db_stored", 0))
	run(fixture.Exec("ALTER TABLE db_stored RENAME COLUMN stamp TO s", 0))
	datetimeBinStoredInsert(t, p, c, b.Recorder, 9)
	datetimeBinCheckWrite(t, p, c, b.Recorder)
	bad := dump.String() + "\nCREATE TABLE db_bad(v timestamp DEFAULT date_bin('0',TIMESTAMP '2000-01-02',TIMESTAMP '2000-01-01')); INSERT INTO db_bad DEFAULT VALUES;"
	d := fixture.ControlsReferenceDiagnostic("22008", "stride must be greater than zero")
	fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)), &d)
	run(fixture.Select("SELECT count(*) FROM db_stored", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled bin restore: %v", err)
	}
	run(fixture.Select("SELECT count(*) FROM db_stored", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
	if p.Postgres != nil {
		pg, err := p.Postgres.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer pg.Close()
		if _, err := pg.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatalf("actual bin replay: %v\n%s", err, dump.String())
		}
		fixture.RunStep(t, p.Ctx, pg, p.PostgresRecord, want)
		fixture.RunStep(t, p.Ctx, pg, p.PostgresRecord, retagWant)
		fixture.RunStep(t, p.Ctx, pg, p.PostgresRecord, fixture.Exec("ALTER TABLE db_renamed RENAME TO db_stored", 0))
		fixture.RunStep(t, p.Ctx, pg, p.PostgresRecord, fixture.Exec("ALTER TABLE db_stored RENAME COLUMN stamp TO s", 0))
		datetimeBinStoredInsert(t, p, pg, p.PostgresRecord, 9)
		datetimeBinCheckWrite(t, p, pg, p.PostgresRecord)
		if _, err := pg.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferenceDatetimeBinStoredConsumers(t *testing.T) {
	t.Run("stored-replay", func(t *testing.T) { datetimeBinScenario(t, datetimeBinStored) })
	t.Run("query-consumers", func(t *testing.T) { datetimeBinScenario(t, datetimeBinQueryConsumers) })
}
