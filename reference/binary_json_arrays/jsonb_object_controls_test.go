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

func jsonbObjectControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, check fixture.Step) {
	t.Helper()
	if b.PG {
		return
	}
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
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE jbo_replacement(v int)", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO jbo_replacement VALUES(99)", 1))
	fixture.StringCoreRetainedRows(t, old, nil, check.Columns, check.Types, check.Rows, false)
	if len(meta) != len(check.Types) {
		t.Fatal(meta)
	}
	for i, ct := range meta {
		if ct.DatabaseTypeName() != check.Types[i] {
			t.Fatal(ct)
		}
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	jsonObjectBoth(t, p, b, c, check)
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
		t.Fatal(err)
	}
	if dump.String() != redump.String() {
		t.Fatalf("redump changed\n%s\n%s", dump.String(), redump.String())
	}
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("actual emitted dump replay", err)
		}
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, check)
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
	unchanged := func() {
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
			t.Fatal(err)
		}
		if after.String() != dump.String() {
			t.Fatal("failed Restore published staged replacement")
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, check)
	}
	for _, tc := range []struct{ expr, code, msg string }{
		{`jsonb_object('{a,b,c}'::text[])`, "2202E", "array must have even number of elements"},
		{`jsonb_object('{NULL}'::text[],'{v}'::text[])`, "22004", "null value not allowed for object key"},
	} {
		script := `CREATE TABLE jbo_bad(v jsonb); INSERT INTO jbo_bad VALUES(jsonb_object('{ok,1}'::text[])); INSERT INTO jbo_bad VALUES(` + tc.expr + `);`
		d := fixture.ControlsReferenceDiagnostic(tc.code, tc.msg)
		fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), &d)
		unchanged()
	}
	for _, expr := range []string{`jsonb_object($1)`, `jsonb_object($1,'{v}'::text[])`, `jsonb_object('{a}'::text[],$1)`, `jsonb_object(ARRAY['a',$1::text])`, `jsonb_object('{a}'::text[],ARRAY[$1::text])`} {
		script := "/*é*/ CREATE TABLE jbo_bad(v jsonb); INSERT INTO jbo_bad VALUES(" + expr + ");"
		d := fixture.ControlsReferenceDiagnostic("42P02", "parameters are not allowed in restore")
		d.Position = fixture.Position(len([]rune(script[:strings.Index(script, "$1")])) + 1)
		fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), &d)
		unchanged()
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	if _, err := b.DB.QueryContext(ctx, "SELECT jsonb_object('{a,1}')"); !errors.Is(err, context.Canceled) {
		t.Fatalf("precanceled query=%v", err)
	}
	valid := `CREATE TABLE jbo_restored(one jsonb,two jsonb); INSERT INTO jbo_restored VALUES(jsonb_object('{a,1}'::text[]),jsonb_object('{a}'::text[],'{1}'::text[]));`
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(valid)); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT one,two FROM jbo_restored", []string{"one", "two"}, []string{"JSONB", "JSONB"}, [][]any{{`{"a": "1"}`, `{"a": "1"}`}}))
}

func TestReferenceJSONBObjectControls(t *testing.T) {
	jsonbObjectScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := jsonbObjectStored(t, p, b, c)
		jsonbObjectControls(t, p, b, c, check)
	})
}
