package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func jsonbContainmentControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, check fixture.Step) {
	t.Helper()
	if b.PG {
		return
	}
	generated := fixture.Select("SELECT id,g FROM jbc_stored ORDER BY id", []string{"id", "g"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), false}, {int64(3), nil}})
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	old, err := c.QueryContext(p.Ctx, check.Query)
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
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE jbc_sentinel(id int)", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO jbc_sentinel VALUES(99)", 1))
	fixture.StringCoreRetainedRows(t, old, nil, check.Columns, check.Types, check.Rows, false)
	for i, column := range metadata {
		if column.Name() != check.Columns[i] || column.DatabaseTypeName() != check.Types[i] {
			t.Fatal("retained metadata", column)
		}
	}
	for range 2 {
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, check)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, generated)
		var again bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil {
			t.Fatal(err)
		}
		if dump.String() != again.String() {
			t.Fatal("containment symbolic replay changed canonical dump")
		}
	}
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("actual emitted containment SQL replay", err)
		}
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, check)
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, generated)
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
	bad := `CREATE TABLE jbc_bad(doc jsonb,g bool GENERATED ALWAYS AS (doc @> '{}'::jsonb) STORED); INSERT INTO jbc_bad(doc) VALUES('{}'); INSERT INTO jbc_bad(doc) VALUES('bad');`
	d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type json")
	d.Detail, d.Context = `Token "bad" is invalid.`, "JSON data, line 1: bad"
	d.Position = fixture.Position(strings.Index(bad, "'bad'") + 1)
	fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)), &d)
	var after bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
		t.Fatal(err)
	}
	if after.String() != dump.String() {
		t.Fatal("late invalid restore replaced catalog or rows")
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, check)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, generated)
}
