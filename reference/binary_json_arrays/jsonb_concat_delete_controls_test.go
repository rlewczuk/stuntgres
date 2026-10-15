package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func jsonbConcatDeleteControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, check fixture.Step) {
	t.Helper()
	if b.PG {
		return
	} // PostgreSQL has no native replacement API.
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	old, err := c.QueryContext(p.Ctx, check.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	meta, err := old.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	jsonTypeRun(t, p, b, c, fixture.Exec("CREATE TABLE jbcd_replacement(v int)", 0))
	jsonTypeRun(t, p, b, c, fixture.Exec("INSERT INTO jbcd_replacement VALUES(99)", 1))
	jsonTypeRun(t, p, b, c, fixture.Select("SELECT v FROM jbcd_replacement", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}}))
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	fixture.StringCoreRetainedRows(t, old, nil, check.Columns, check.Types, check.Rows, false)
	if len(meta) != len(check.Types) {
		t.Fatal(meta)
	}
	for i, ct := range meta {
		if ct.DatabaseTypeName() != check.Types[i] {
			t.Fatal(ct)
		}
	}
	unchanged := func() {
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
			t.Fatal(err)
		}
		if after.String() != dump.String() {
			t.Fatalf("redump changed\nbefore:\n%s\nafter:\n%s", dump.String(), after.String())
		}
		jsonTypeRun(t, p, b, c, check)
	}
	unchanged()
	bad := `CREATE TABLE jbcd_restore(v jsonb); INSERT INTO jbcd_restore VALUES(('{}'::jsonb || '{"a":1}') - 'a'); INSERT INTO jbcd_restore VALUES('{}'::jsonb - 0);`
	d := fixture.ControlsReferenceDiagnostic("22023", "cannot delete from object using integer index")
	fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)), &d)
	unchanged()
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	fixture.LexicalOrdinary(t, stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())), context.Canceled)
	unchanged()
	for _, expr := range []string{"$1::jsonb || '{}'::jsonb", "'{}'::jsonb - $1::text", "'[]'::jsonb - $1::integer"} {
		script := "/*é*/ CREATE TABLE jbcd_restore(v jsonb); INSERT INTO jbcd_restore VALUES(" + expr + ");"
		d := fixture.ControlsReferenceDiagnostic("42P02", "parameters are not allowed in restore")
		d.Position = fixture.Position(len([]rune(script[:strings.Index(script, "$1")])) + 1)
		fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), &d)
		unchanged()
	}
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("actual unmodified dump replay", err)
		}
		pg := fixture.Backend{PG: true, Recorder: p.PostgresRecord}
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, check)
		jsonbConcatDeleteFresh(t, p, pg, p.Postgres)
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec("INSERT INTO jbcd_default(id) VALUES(2)", 1))
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, jsonbConcatDeleteSelect("SELECT v FROM jbcd_default WHERE id=2", `{"b": 2}`))
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
	jsonbConcatDeleteFresh(t, p, b, c)
	jsonTypeRun(t, p, b, c, fixture.Exec("INSERT INTO jbcd_default(id) VALUES(2)", 1))
	jsonTypeBoth(t, p, b, c, jsonbConcatDeleteSelect("SELECT v FROM jbcd_default WHERE id=2", `{"b": 2}`))
	jsonTypeRun(t, p, b, c, fixture.Exec(`INSERT INTO jbcd_check VALUES('{"a":9}')`, 1))
}
