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

func extractStored(t *testing.T, p *fixture.Pair, b fixture.Backend) []fixture.Step {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE extract_stored(id integer,v uuid,ts timestamptz GENERATED ALWAYS AS (uuid_extract_timestamp(v)) STORED,ver smallint GENERATED ALWAYS AS (uuid_extract_version(v)) STORED,d smallint DEFAULT uuid_extract_version('"+extractB+"'),CONSTRAINT extract_allowed CHECK (uuid_extract_version(v) <> 5))", 0),
		fixture.Exec("CREATE TABLE extract_empty(v uuid)", 0),
		fixture.Exec("CREATE INDEX extract_version_idx ON extract_stored (uuid_extract_version(v))", 0),
		fixture.Exec("CREATE VIEW extract_view AS SELECT id,CASE WHEN uuid_extract_version(v)=4 THEN NULL ELSE uuid_extract_timestamp(v) END AS ts,COALESCE(uuid_extract_version(v),0) AS ver FROM extract_stored", 0),
		fixture.Exec("INSERT INTO extract_stored(id,v) VALUES (1,'"+extractA+"'),(2,'"+extractB+"'),(3,'"+extractC+"'),(4,NULL)", 4),
	} {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	}
	checks := []fixture.Step{
		extractStep("SELECT uuid_extract_timestamp(v) AS ts,uuid_extract_version(v) AS ver FROM extract_stored ORDER BY id", [][]any{{extractTime, int64(1)}, {extractTime, int64(7)}, {nil, int64(4)}, {nil, nil}}),
		fixture.Select("SELECT ts,ver,d FROM extract_stored ORDER BY id", []string{"ts", "ver", "d"}, []string{"TIMESTAMPTZ", "INT2", "INT2"}, [][]any{{extractTime, int64(1), int64(7)}, {extractTime, int64(7), int64(7)}, {nil, int64(4), int64(7)}, {nil, nil, int64(7)}}),
		fixture.Select("SELECT ts,ver FROM extract_view ORDER BY id", []string{"ts", "ver"}, []string{"TIMESTAMPTZ", "INT4"}, [][]any{{extractTime, int64(1)}, {extractTime, int64(7)}, {nil, int64(4)}, {nil, int64(0)}}),
		extractStep("SELECT uuid_extract_timestamp(v) AS ts,uuid_extract_version(v) AS ver FROM extract_empty", nil),
	}
	for _, s := range checks {
		extractRunBoth(t, p, b, s)
	}
	grouped := fixture.Select("SELECT uuid_extract_version(v) AS ver,COUNT(*) AS n FROM extract_stored GROUP BY uuid_extract_version(v)", []string{"ver", "n"}, []string{"INT2", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(7), int64(1)}, {int64(4), int64(1)}, {nil, int64(1)}})
	grouped.Bag = true
	extractRunBoth(t, p, b, grouped)
	extractRunBoth(t, p, b, extractStep("SELECT uuid_extract_timestamp(a.v) AS ts,uuid_extract_version(a.v) AS ver FROM extract_stored a JOIN extract_stored b ON uuid_extract_version(a.v)=b.id WHERE b.id=1", [][]any{{extractTime, int64(1)}}))
	subquery := extractStep("SELECT uuid_extract_timestamp(v) AS ts,uuid_extract_version(v) AS ver FROM extract_stored WHERE id IN (SELECT uuid_extract_version(v) FROM extract_stored)", [][]any{{extractTime, int64(1)}, {nil, nil}})
	subquery.Bag = true
	extractRunBoth(t, p, b, subquery)
	return checks
}

func extractFutureWrite(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	q := "INSERT INTO extract_stored(id,v) VALUES (5,$1)"
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(fixture.Exec(q, 1, extractB)))
	extractRunBoth(t, p, b, fixture.Select("SELECT ts,ver,d FROM extract_stored WHERE id=5", []string{"ts", "ver", "d"}, []string{"TIMESTAMPTZ", "INT2", "INT2"}, [][]any{{extractTime, int64(7), int64(7)}}))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DELETE FROM extract_stored WHERE id=5", 1))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 1, extractA))
	extractRunBoth(t, p, b, fixture.Select("SELECT ts,ver,d FROM extract_stored WHERE id=5", []string{"ts", "ver", "d"}, []string{"TIMESTAMPTZ", "INT2", "INT2"}, [][]any{{extractTime, int64(1), int64(7)}}))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DELETE FROM extract_stored WHERE id=5", 1))
}

func extractAtomicity(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE extract_atomic(id integer,ver smallint NOT NULL,CONSTRAINT extract_atomic_allowed CHECK (ver<>5))", 0))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO extract_atomic VALUES (99,7)", 1))
	q := "INSERT INTO extract_atomic SELECT id,uuid_extract_version(v) FROM extract_stored ORDER BY id"
	for _, prepared := range []bool{false, true} {
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "23502", Message: `null value in column "ver" of relation "extract_atomic" violates not-null constraint`, Detail: "Failing row contains (4, null).", Schema: "public", Table: "extract_atomic", Column: "ver"})
		s.Prepared, s.ErrorStage = prepared, "execute"
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		extractRunBoth(t, p, b, fixture.Select("SELECT id,ver FROM extract_atomic", []string{"id", "ver"}, []string{"INT4", "INT2"}, [][]any{{int64(99), int64(7)}}))
		qbad := "INSERT INTO extract_atomic VALUES (1,uuid_extract_version($1))"
		s = fixture.ImplicitError(qbad, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type uuid: "bad"`, Context: "unnamed portal parameter $1 = '...'"}, "bad")
		s.Prepared, s.ErrorStage = prepared, "execute"
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		extractRunBoth(t, p, b, fixture.Select("SELECT id,ver FROM extract_atomic", []string{"id", "ver"}, []string{"INT4", "INT2"}, [][]any{{int64(99), int64(7)}}))
		checkQuery := "INSERT INTO extract_atomic VALUES (1,uuid_extract_version('00000000-0000-5000-8000-000000000000'))"
		check := fixture.ImplicitError(checkQuery, fixture.Diagnostic{Code: "23514", Message: `new row for relation "extract_atomic" violates check constraint "extract_atomic_allowed"`, Detail: "Failing row contains (1, 5).", Schema: "public", Table: "extract_atomic", Constraint: "extract_atomic_allowed"})
		check.Prepared, check.ErrorStage = prepared, "execute"
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, check)
		extractRunBoth(t, p, b, fixture.Select("SELECT id,ver FROM extract_atomic", []string{"id", "ver"}, []string{"INT4", "INT2"}, [][]any{{int64(99), int64(7)}}))
	}
}

func TestReferenceUUIDExtractStored(t *testing.T) {
	extractScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		extractStored(t, p, b)
		extractFutureWrite(t, p, b)
		extractAtomicity(t, p, b)
	})
}

func extractControls(t *testing.T, p *fixture.Pair, b fixture.Backend, checks []fixture.Step) {
	t.Helper()
	if b.PG {
		return
	} // Native API phase; SQL recipe above is shared by both backends.
	held, err := b.DB.QueryContext(p.Ctx, checks[0].Query)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	columns, err := held.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	extractMetadata(t, b, columns)
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"uuid_extract_timestamp(", "uuid_extract_version(", "extract_version_idx", "extract_view"} {
		if !strings.Contains(dump.String(), fragment) {
			t.Fatal("missing stored recipe", fragment, dump.String())
		}
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	checks[0].CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		m, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		extractMetadata(t, b, m)
	}
	fixture.RangeConsumeHeld(t, held, checks[0])
	extractMetadata(t, b, columns)
	for _, s := range checks {
		extractRunBoth(t, p, b, s)
	}
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
		t.Fatal("redump differs", err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	extractFutureWrite(t, p, b)
	target := fixture.NewFixture(t, false, true)
	for _, q := range []string{"CREATE TABLE spacer(v text)", "DROP TABLE spacer", "CREATE TABLE sentinel(v integer)"} {
		if _, err := target.Memory.ExecContext(target.Ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	tb := fixture.Backend{DB: target.Memory, Recorder: target.MemoryRecord}
	for _, s := range checks {
		extractRunBoth(t, target, tb, s)
	}
	extractFutureWrite(t, target, tb)
	for _, script := range []string{
		"CREATE TABLE replacement(v uuid); INSERT INTO replacement VALUES ('" + extractA + "'); INSERT INTO replacement VALUES ('bad');",
		"CREATE TABLE replacement(v smallint DEFAULT uuid_extract_version($1));",
	} {
		code, message, at := "22P02", `invalid input syntax for type uuid: "bad"`, "'bad'"
		if strings.Contains(script, "$1") {
			code, message, at = "42P02", "parameters are not allowed in restore", "$1"
		}
		want := extractDiagnostic(script, code, message, "", at, false)
		fixture.AssertDiagnostic(t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(script)), &want)
		for _, s := range checks {
			extractRunBoth(t, target, tb, s)
		}
		redump.Reset()
		if err := stuntgres.Dump(target.Ctx, target.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
			t.Fatal("failed restore published", err)
		}
	}
	failure := errors.New("extraction restore read failure")
	if err := stuntgres.Restore(target.Ctx, target.Native, fixture.CatalogControlFailingReader{Err: failure}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(target.Ctx)
	cancel()
	if err := stuntgres.Restore(ctx, target.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, s := range checks {
		extractRunBoth(t, target, tb, s)
	}
	old, err := target.Memory.QueryContext(target.Ctx, checks[0].Query)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	oldColumns, err := old.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	extractMetadata(t, tb, oldColumns)
	if err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader("CREATE TABLE replacement(v text); INSERT INTO replacement VALUES ('new');")); err != nil {
		t.Fatal(err)
	}
	fixture.RangeConsumeHeld(t, old, checks[0])
	extractMetadata(t, tb, oldColumns)
	extractRunBoth(t, target, tb, fixture.Select("SELECT v FROM replacement", []string{"v"}, []string{"TEXT"}, [][]any{{"new"}}))
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("emitted SQL replay", err, dump.String())
		}
		pg := fixture.Backend{DB: p.Postgres, Recorder: p.PostgresRecord, PG: true}
		for _, s := range checks {
			s.CheckMetadata = nil
			extractRunBoth(t, p, pg, s)
		}
		extractFutureWrite(t, p, pg)
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferenceUUIDExtractControls(t *testing.T) {
	extractScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		checks := extractStored(t, p, b)
		extractControls(t, p, b, checks)
	})
}

func TestReferenceUUIDExtractFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"uuid_extract_reader"})
	extractScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		extractBase(t, p, b)
		extractBinds(t, p, b)
		extractTimestamps(t, p, b)
		extractVersions(t, p, b)
		extractAuthorization(t, p, b)
		checks := extractStored(t, p, b)
		extractControls(t, p, b, checks)
	})
}
