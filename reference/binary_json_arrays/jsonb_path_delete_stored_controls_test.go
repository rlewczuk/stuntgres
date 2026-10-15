package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func jsonbPathDeleteStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) fixture.Step {
	t.Helper()
	run := func(s fixture.Step) { jsonTypeRun(t, p, b, c, s) }
	for _, q := range []string{
		`CREATE TABLE jbd_source(id int,doc jsonb,path text[],changed jsonb GENERATED ALWAYS AS (doc #- path) STORED)`,
		`CREATE TABLE jbd_default(id int,v jsonb DEFAULT ('{"a":1,"b":2}'::jsonb #- '{a}'))`,
		`CREATE TABLE jbd_check(doc jsonb,CONSTRAINT jbd_empty CHECK(doc #- '{a}' = '{}'::jsonb))`,
		`CREATE VIEW jbd_view AS SELECT id,doc #- path AS changed FROM jbd_source`,
		`CREATE INDEX jbd_idx ON jbd_source ((doc #- path))`,
	} {
		run(fixture.Exec(q, 0))
	}
	run(fixture.Exec(`INSERT INTO jbd_source(id,doc,path) VALUES(1,'{"a":1,"b":2}','{a}'),(2,'{"a":1}','{missing}'),(3,NULL,'{a}')`, 3))
	run(fixture.Exec("INSERT INTO jbd_default(id) VALUES(1)", 1))
	run(fixture.Exec(`INSERT INTO jbd_check VALUES('{"a":1}')`, 1))
	check := fixture.Select("SELECT id,doc,path,changed FROM jbd_source ORDER BY id", []string{"id", "doc", "path", "changed"}, []string{"INT4", "JSONB", "_TEXT", "JSONB"}, [][]any{
		{int64(1), `{"a": 1, "b": 2}`, "{a}", `{"b": 2}`},
		{int64(2), `{"a": 1}`, "{missing}", `{"a": 1}`},
		{int64(3), nil, "{a}", nil},
	})
	jsonTypeBoth(t, p, b, c, check)
	jsonTypeBoth(t, p, b, c, fixture.Select("SELECT id,changed FROM jbd_view ORDER BY id", []string{"id", "changed"}, []string{"INT4", "JSONB"}, [][]any{{int64(1), `{"b": 2}`}, {int64(2), `{"a": 1}`}, {int64(3), nil}}))
	jsonTypeBoth(t, p, b, c, fixture.Select("SELECT pg_get_viewdef('jbd_view'::regclass) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{" SELECT id,\n    (doc #- path) AS changed\n   FROM jbd_source;"}}))
	jsonTypeBoth(t, p, b, c, fixture.Select("SELECT id,v FROM jbd_default ORDER BY id", []string{"id", "v"}, []string{"INT4", "JSONB"}, [][]any{{int64(1), `{"b": 2}`}}))
	bad := fixture.ErrorDiagnostic(`INSERT INTO jbd_source(id,doc,path) VALUES(4,'{"a":2}','{a}'),(5,'[1]','{x}')`, fixture.ControlsReferenceDiagnostic("22P02", `path element at position 1 is not an integer: "x"`))
	jsonTypeBoth(t, p, b, c, bad)
	run(check)
	badUpdate := fixture.ErrorDiagnostic("UPDATE jbd_source SET doc='[1]',path=CASE WHEN id=1 THEN '{0}'::text[] ELSE '{x}'::text[] END WHERE id<3", fixture.ControlsReferenceDiagnostic("22P02", `path element at position 1 is not an integer: "x"`))
	jsonTypeBoth(t, p, b, c, badUpdate)
	run(check)
	d := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "jbd_check" violates check constraint "jbd_empty"`)
	d.Detail = `Failing row contains ({"b": 2}).`
	d.Schema, d.Table, d.Constraint = "public", "jbd_check", "jbd_empty"
	jsonTypeBoth(t, p, b, c, fixture.ErrorDiagnostic(`INSERT INTO jbd_check VALUES('{"b":2}')`, d))
	jsonTypeBoth(t, p, b, c, fixture.Select("SELECT doc AS v FROM jbd_check", []string{"v"}, []string{"JSONB"}, [][]any{{`{"a": 1}`}}))
	run(fixture.Exec(`INSERT INTO jbd_source(id,doc,path) VALUES(4,'[1,2]','{-1}')`, 1))
	run(fixture.Exec("DELETE FROM jbd_source WHERE id=4", 1))
	return check
}

func jsonbPathDeleteFresh(t *testing.T, p *fixture.Pair, c fixture.Executor, recorder *fixture.Recorder) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, recorder, fixture.Exec(`INSERT INTO jbd_source(id,doc,path) VALUES(4,'[1,2]','{-1}')`, 1))
	fixture.RunStep(t, p.Ctx, c, recorder, fixture.Select("SELECT changed AS v FROM jbd_source WHERE id=4", []string{"v"}, []string{"JSONB"}, [][]any{{`[1]`}}))
	fixture.RunStep(t, p.Ctx, c, recorder, fixture.Select("SELECT changed AS v FROM jbd_view WHERE id=4", []string{"v"}, []string{"JSONB"}, [][]any{{`[1]`}}))
	fixture.RunStep(t, p.Ctx, c, recorder, fixture.Exec("INSERT INTO jbd_default(id) VALUES(2)", 1))
	fixture.RunStep(t, p.Ctx, c, recorder, fixture.Select("SELECT v FROM jbd_default WHERE id=2", []string{"v"}, []string{"JSONB"}, [][]any{{`{"b": 2}`}}))
	fixture.RunStep(t, p.Ctx, c, recorder, fixture.Exec(`INSERT INTO jbd_check VALUES('{"a":9}')`, 1))
}

func jsonbPathDeleteControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, check fixture.Step) {
	t.Helper()
	if b.PG {
		return
	} // Only native exposes Reset/Dump/Restore.
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
	jsonTypeRun(t, p, b, c, fixture.Exec("CREATE TABLE jbd_replacement(v int)", 0))
	jsonTypeRun(t, p, b, c, fixture.Exec("INSERT INTO jbd_replacement VALUES(99)", 1))
	jsonTypeRun(t, p, b, c, fixture.Select("SELECT v FROM jbd_replacement", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}}))
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
	unchanged := func() {
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
			t.Fatal(err)
		}
		if after.String() != dump.String() {
			t.Fatalf("dump changed\nbefore:\n%s\nafter:\n%s", dump.String(), after.String())
		}
		jsonTypeRun(t, p, b, c, check)
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
		jsonbPathDeleteFresh(t, p, p.Postgres, p.PostgresRecord)
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
	script := `CREATE TABLE jbd_bad(v jsonb); INSERT INTO jbd_bad VALUES('{}'::jsonb #- '{}'); INSERT INTO jbd_bad VALUES('[1]'::jsonb #- '{x}');`
	d := fixture.ControlsReferenceDiagnostic("22P02", `path element at position 1 is not an integer: "x"`)
	fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), &d)
	unchanged()
	for _, expr := range []string{"$1 #- '{}'::text[]", "'{}'::jsonb #- $1"} {
		script := "/*é*/ CREATE TABLE jbd_bad(v jsonb); INSERT INTO jbd_bad VALUES(" + expr + ");"
		d := fixture.ControlsReferenceDiagnostic("42P02", "parameters are not allowed in restore")
		d.Position = fixture.Position(len([]rune(script[:strings.Index(script, "$1")])) + 1)
		fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), &d)
		unchanged()
	}
	jsonbPathDeleteFresh(t, p, c, b.Recorder)
}

func TestReferenceJSONBPathDeleteStoredControls(t *testing.T) {
	jsonbPathDeleteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := jsonbPathDeleteStored(t, p, b, c)
		jsonbPathDeleteControls(t, p, b, c, check)
		if b.PG {
			jsonbPathDeleteFresh(t, p, c, b.Recorder)
		}
	})
}
