package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonbSetControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, check fixture.Step) {
	t.Helper()
	if b.PG {
		return
	} // PostgreSQL has no native control API.
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
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE jbs_replacement(v int)", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO jbs_replacement VALUES(99)", 1))
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
	jsonbSetBoth(t, p, b, c, check)
	unchanged := func() {
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
			t.Fatal(err)
		}
		if after.String() != dump.String() {
			t.Fatalf("Restore changed persistent dump\nbefore:\n%s\nafter:\n%s", dump.String(), after.String())
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, check)
	}
	unchanged()
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("actual dump replay", err)
		}
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, check)
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Select("SELECT id,changed FROM jbs_view ORDER BY id", []string{"id", "changed"}, []string{"INT4", "JSONB"}, [][]any{{int64(1), `{}`}, {int64(2), `{}`}, {int64(3), nil}}))
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Select("SELECT id,changed FROM jbs_variadic ORDER BY id", []string{"id", "changed"}, []string{"INT4", "JSONB"}, [][]any{{int64(1), `{"a": 9}`}, {int64(2), `{"a": 9}`}, {int64(3), nil}}))
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		expr string
		d    fixture.Diagnostic
	}{
		{`jsonb_set('[1]','{x}','2')`, fixture.ControlsReferenceDiagnostic("22P02", `path element at position 1 is not an integer: "x"`)},
		{`jsonb_set_lax('{}','{a}',NULL,true,'raise_exception')`, jsonbSetRaise()},
	} {
		script := "CREATE TABLE jbs_bad(v jsonb); INSERT INTO jbs_bad VALUES(jsonb_set('{}','{a}','1')); INSERT INTO jbs_bad VALUES(" + tc.expr + ");"
		fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), &tc.d)
		unchanged()
	}
	for slot := range 5 {
		args := []string{`'{}'`, `'{a}'`, `NULL`, `true`, `'use_json_null'`}
		args[slot] = "$1"
		if slot == 4 {
			args[slot] = "CASE WHEN TRUE THEN $1 ELSE 'use_json_null' END"
		}
		script := "/*é*/ CREATE TABLE jbs_bad(v jsonb); INSERT INTO jbs_bad VALUES(jsonb_set_lax(" + strings.Join(args, ",") + "));"
		d := fixture.ControlsReferenceDiagnostic("42P02", "parameters are not allowed in restore")
		d.Position = fixture.Position(len([]rune(script[:strings.Index(script, "$1")])) + 1)
		fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), &d)
		unchanged()
	}
}

func TestReferenceJSONBSetControls(t *testing.T) {
	jsonbSetScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := jsonbSetStored(t, p, b, c)
		jsonbSetControls(t, p, b, c, check)
	})
}
