package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func datetimeExtractStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE DOMAIN dx_year AS numeric", 0))
	run(fixture.Exec("CREATE TABLE dx_stored(id integer,s timestamp,y numeric GENERATED ALWAYS AS (EXTRACT(year FROM s)) STORED,d numeric DEFAULT EXTRACT(doy FROM DATE '2000-02-29'),z float8 DEFAULT date_part('hour',TIMESTAMPTZ '2020-01-01 00:00+00'),CONSTRAINT dx_positive CHECK(NOT(EXTRACT(year FROM s)=1999)))", 0))
	run(fixture.Exec("CREATE TABLE dx_domain(v dx_year,CONSTRAINT dx_year_check CHECK(EXTRACT(year FROM DATE '2000-01-01')=v))", 0))
	run(fixture.Exec("INSERT INTO dx_domain VALUES(2000)", 1))
	domainError := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "dx_domain" violates check constraint "dx_year_check"`)
	domainError.Detail, domainError.Schema, domainError.Table, domainError.Constraint = "Failing row contains (2001).", "public", "dx_domain", "dx_year_check"
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic("INSERT INTO dx_domain VALUES(2000),(2001)", domainError)
		s.Prepared, s.ErrorStage = prepared, "execute"
		run(s)
		run(fixture.Select("SELECT v FROM dx_domain", []string{"v"}, []string{"NUMERIC"}, [][]any{{"2000"}}))
	}
	run(fixture.Exec("CREATE TABLE dx_immutable(d date,i interval,y numeric GENERATED ALWAYS AS (EXTRACT(year FROM d)) STORED,m float8 GENERATED ALWAYS AS (date_part('month',i)) STORED)", 0))
	run(fixture.Exec("CREATE INDEX dx_date_index ON dx_immutable ((EXTRACT(year FROM d)))", 0))
	run(fixture.Exec("CREATE INDEX dx_interval_index ON dx_immutable ((date_part('month',i)))", 0))
	run(fixture.Exec("INSERT INTO dx_immutable(d,i) VALUES(DATE '2000-02-29',INTERVAL '2 years 13 months')", 1))
	run(fixture.Select("SELECT y,m FROM dx_immutable", []string{"y", "m"}, []string{"NUMERIC", "FLOAT8"}, [][]any{{"2000", float64(1)}}))
	run(fixture.Exec("CREATE INDEX dx_expr ON dx_stored ((EXTRACT(year FROM s)))", 0))
	run(fixture.Exec("INSERT INTO dx_stored(id,s) VALUES(1,TIMESTAMP '2000-02-29'),(2,TIMESTAMP '2001-01-01')", 2))
	run(fixture.Exec("CREATE VIEW dx_view AS SELECT id,EXTRACT(year FROM s) AS year,date_part('month',s) AS month,CASE WHEN true THEN EXTRACT('year' FROM s) ELSE EXTRACT('bad''\\é' FROM s) END AS safe FROM dx_stored", 0))
	want := fixture.Select("SELECT id,year,month,safe FROM dx_view ORDER BY id", []string{"id", "year", "month", "safe"}, []string{"INT4", "NUMERIC", "FLOAT8", "NUMERIC"}, [][]any{{int64(1), "2000", float64(2), "2000"}, {int64(2), "2001", float64(1), "2001"}})
	datetimeExtractBoth(t, p, b, c, want)
	datetimeExtractBoth(t, p, b, c, fixture.Select("SELECT EXTRACT(year FROM s) AS year,count(*) AS n FROM dx_stored GROUP BY EXTRACT(year FROM s) ORDER BY EXTRACT(year FROM s)", []string{"year", "n"}, []string{"NUMERIC", "INT8"}, [][]any{{"2000", int64(1)}, {"2001", int64(1)}}))
	datetimeExtractBoth(t, p, b, c, fixture.Select("SELECT COUNT(EXTRACT(month FROM s)) AS total FROM dx_stored WHERE date_part('year',s)=2000 OR date_part('year',s)=2001", []string{"total"}, []string{"INT8"}, [][]any{{int64(2)}}))
	sumQuery := "SELECT SUM(EXTRACT(month FROM s)) AS total FROM dx_stored WHERE date_part('year',s)=2000 OR date_part('year',s)=2001"
	datetimeExtractBoth(t, p, b, c, fixture.Select(sumQuery, []string{"total"}, []string{"NUMERIC"}, [][]any{{"3"}}))
	datetimeExtractBoth(t, p, b, c, fixture.Select("SELECT a.id FROM dx_stored a JOIN dx_stored b ON EXTRACT(year FROM a.s)=EXTRACT(year FROM b.s) ORDER BY a.id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
	datetimeExtractPair(t, p, b, c, "year", "COALESCE(NULL::timestamp,TIMESTAMP '2000-01-01')", "2000", 2000)
	run(fixture.Exec("ALTER TABLE dx_stored RENAME COLUMN s TO stamp", 0))
	run(fixture.Exec("ALTER TABLE dx_stored RENAME TO dx_renamed", 0))
	run(want)
	run(fixture.Exec("CREATE OR REPLACE VIEW dx_view AS SELECT id,EXTRACT(year FROM stamp) AS year,date_part('month',stamp) AS month,CASE WHEN true THEN EXTRACT('year' FROM stamp) ELSE EXTRACT('bad''\\é' FROM stamp) END AS safe FROM dx_renamed", 0))
	run(want)
	// PG's canonical renderer emits unusual fields raw (not round-trippable).
	// Native logical Dump below independently must encode them safely for replay.
	rows, err := c.QueryContext(p.Ctx, "SELECT pg_get_viewdef('dx_view'::regclass,true)")
	if err != nil {
		t.Fatal(err)
	}
	var definition string
	if !rows.Next() {
		rows.Close()
		t.Fatalf("view definition: %v", rows.Err())
	}
	if err := rows.Scan(&definition); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	if rows.Next() || rows.Err() != nil {
		rows.Close()
		t.Fatal("extra view definition")
	}
	rows.Close()
	if !strings.Contains(definition, "EXTRACT(year FROM") || !strings.Contains(definition, "date_part(") {
		t.Fatalf("extraction provenance lost: %s", definition)
	}
	for _, tc := range []struct{ q, message string }{
		{"CREATE TABLE dx_stable(t timestamptz,y numeric GENERATED ALWAYS AS (EXTRACT(year FROM t)) STORED)", "generation expression is not immutable"},
		{"CREATE INDEX dx_stable_index ON dx_renamed ((EXTRACT(year FROM stamp::timestamptz)))", "functions in index expression must be marked IMMUTABLE"},
		{"CREATE TABLE dx_volatile(t timestamp,y float8 GENERATED ALWAYS AS (date_part('year',t)+random()) STORED)", "generation expression is not immutable"},
	} {
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(tc.q, fixture.ControlsReferenceDiagnostic("42P17", tc.message))
			s.Prepared, s.ErrorStage = prepared, "execute"
			run(s)
		}
	}
	run(fixture.Exec("SET TIME ZONE 'America/New_York'", 0))
	run(fixture.Exec("INSERT INTO dx_renamed(id,stamp) VALUES(3,TIMESTAMP '2002-01-01')", 1))
	run(fixture.Select("SELECT d,z FROM dx_renamed ORDER BY id", []string{"d", "z"}, []string{"NUMERIC", "FLOAT8"}, [][]any{{"60", float64(0)}, {"60", float64(0)}, {"60", float64(19)}}))
	run(fixture.Select("UPDATE dx_renamed SET stamp=TIMESTAMP '2003-01-01' WHERE id=3 RETURNING EXTRACT(year FROM stamp) AS year", []string{"year"}, []string{"NUMERIC"}, [][]any{{"2003"}}))
	run(fixture.Select("DELETE FROM dx_renamed WHERE id=3 RETURNING date_part('year',stamp) AS year", []string{"year"}, []string{"FLOAT8"}, [][]any{{float64(2003)}}))
	if b.PG {
		return
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	held, err := c.PrepareContext(p.Ctx, want.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	heldExecutor := fixture.HeldStatement{Executor: c, Statement: held, SQL: want.Query}
	destination, wrapped, record := timezoneDestination(t)
	if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	old, err := p.Native.QueryContext(p.Ctx, want.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	metadata, err := old.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	// Destination descriptors must survive replacement of their source provider.
	fixture.RunStep(t, p.Ctx, wrapped, record, want)
	fixture.RunStep(t, p.Ctx, wrapped, record, fixture.Exec("INSERT INTO dx_renamed(id,stamp) VALUES(9,TIMESTAMP '2009-01-01')", 1))
	fixture.RunStep(t, p.Ctx, wrapped, record, fixture.Select("SELECT y,d FROM dx_renamed WHERE id=9", []string{"y", "d"}, []string{"NUMERIC", "NUMERIC"}, [][]any{{"2009", "60"}}))
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	for _, expected := range want.Rows {
		if !old.Next() {
			t.Fatalf("retained row: %v", old.Err())
		}
		var id, year, month, safe any
		if err := old.Scan(&id, &year, &month, &safe); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual([]any{id, year, month, safe}, expected) {
			t.Fatalf("retained %#v want %#v", []any{id, year, month, safe}, expected)
		}
	}
	if old.Next() || old.Err() != nil {
		t.Fatal("retained tail")
	}
	old.Close()
	if metadata[1].DatabaseTypeName() != "NUMERIC" || metadata[2].DatabaseTypeName() != "FLOAT8" {
		t.Fatal("retained descriptor lost")
	}
	run(want)
	fixture.RunStep(t, p.Ctx, heldExecutor, b.Recorder, want)
	bad := dump.String() + "\nCREATE TABLE dx_bad(v numeric DEFAULT EXTRACT(bogus FROM DATE '2000-01-01')); INSERT INTO dx_bad DEFAULT VALUES;"
	d := datetimeExtractFieldDiagnostic("22023", "bogus", "date")
	fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)), &d)
	run(want)
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled restore: %v", err)
	}
	failure := errors.New("extract restore reader failure")
	if err := stuntgres.Restore(p.Ctx, p.Native, timezoneFailingReader{failure}); !errors.Is(err, failure) {
		t.Fatalf("reader error: %v", err)
	}
	run(want)
	run(fixture.Exec("INSERT INTO dx_renamed(id,stamp) VALUES(4,TIMESTAMP '2004-01-01')", 1))
	run(fixture.Select("SELECT y,d FROM dx_renamed WHERE id=4", []string{"y", "d"}, []string{"NUMERIC", "NUMERIC"}, [][]any{{"2004", "60"}}))
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatalf("actual native SQL replay: %v\n%s", err, dump.String())
		}
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, want)
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferenceDatetimeExtractStoredConsumers(t *testing.T) {
	datetimeExtractScenario(t, datetimeExtractStored)
}
