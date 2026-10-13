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

func jsonObjectControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, check fixture.Step) {
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
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE jo_replacement(v int)", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO jo_replacement VALUES(99)", 1))
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
		{`json_object('{a,b,c}'::text[])`, "2202E", "array must have even number of elements"},
		{`json_object('{NULL}'::text[],'{v}'::text[])`, "22004", "null value not allowed for object key"},
	} {
		script := `CREATE TABLE jo_bad(v json); INSERT INTO jo_bad VALUES(json_object('{ok,1}'::text[])); INSERT INTO jo_bad VALUES(` + tc.expr + `);`
		d := fixture.ControlsReferenceDiagnostic(tc.code, tc.msg)
		fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), &d)
		unchanged()
	}
	for _, expr := range []string{`json_object($1)`, `json_object($1,'{v}'::text[])`, `json_object('{a}'::text[],$1)`, `json_object(ARRAY['a',$1::text])`, `json_object('{a}'::text[],ARRAY[$1::text])`} {
		script := "/*é*/ CREATE TABLE jo_bad(v json); INSERT INTO jo_bad VALUES(" + expr + ");"
		d := fixture.ControlsReferenceDiagnostic("42P02", "parameters are not allowed in restore")
		d.Position = fixture.Position(len([]rune(script[:strings.Index(script, "$1")])) + 1)
		fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), &d)
		unchanged()
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	if _, err := b.DB.QueryContext(ctx, "SELECT json_object('{a,1}')"); !errors.Is(err, context.Canceled) {
		t.Fatalf("precanceled query=%v", err)
	}
	valid := `CREATE TABLE jo_restored(one json,two json); INSERT INTO jo_restored VALUES(json_object('{a,1}'::text[]),json_object('{a}'::text[],'{1}'::text[]));`
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(valid)); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT one,two FROM jo_restored", []string{"one", "two"}, []string{"JSON", "JSON"}, [][]any{{`{"a" : "1"}`, `{"a" : "1"}`}}))
}

func TestReferenceJSONObjectControls(t *testing.T) {
	jsonObjectScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := jsonObjectStored(t, p, b, c)
		jsonObjectControls(t, p, b, c, check)
	})
}

func TestReferenceJSONObjectFeatureSmoke(t *testing.T) {
	jsonObjectScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := jsonObjectStored(t, p, b, c)
		for _, expr := range []string{`json_object('{a,1,b,2}')`, `json_object('{{a,1},{b,2}}')`, `json_object('[-2:-1]={a,b}'::text[],'[7:8]={1,2}'::text[])`} {
			s := jsonObjectSelect("SELECT "+expr+" AS v", `{"a" : "1", "b" : "2"}`)
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) { jsonObjectMetadata(t, r, b.PG, "v") }
			jsonObjectBoth(t, p, b, c, s)
		}
		for _, q := range []string{"SELECT json_object($1) AS v", "SELECT json_object($1,$2) AS v"} {
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			args := []any{"{a,NULL,a,2}"}
			null := []any{nil}
			bad := []any{"{odd}"}
			msg := "array must have even number of elements"
			if q == "SELECT json_object($1,$2) AS v" {
				args = []any{"{a,a}", "{NULL,2}"}
				null = []any{nil, "{x}"}
				bad = []any{"{a,b}", "{x}"}
				msg = "mismatched array dimensions"
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonObjectSelect(q, `{"a" : null, "a" : "2"}`, args...))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonObjectSelect(q, nil, null...))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonObjectError(q, "2202E", msg, "execute", bad...))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonObjectSelect(q, `{"a" : null, "a" : "2"}`, args...))
			stmt.Close()
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("UPDATE jo_stored SET a='{b,2}',k='{b}',v='{2}' WHERE id=1", 1))
		check.Rows = [][]any{{int64(1), `{"b" : "2"}`, `{"b" : "2"}`}, {int64(2), `{"a" : "1"}`, `{"a" : "1"}`}}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, check)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic("INSERT INTO jo_stored(id,a) VALUES(3,'{ok,3}'),(4,'{odd}')", fixture.ControlsReferenceDiagnostic("2202E", "array must have even number of elements")))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, check)
		jsonObjectControls(t, p, b, c, check)
	})
}
