package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonbStripStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) fixture.Step {
	t.Helper()
	for _, q := range []string{
		`CREATE TABLE jbs_stored(id int,v jsonb DEFAULT jsonb_strip_nulls('{"x":null,"a":[null,1]}'),flag bool DEFAULT true,CONSTRAINT jbs_clean CHECK (jsonb_strip_nulls(v,flag)::text <> '"bad"'))`,
		`CREATE VIEW jbs_named AS SELECT id,jsonb_strip_nulls(strip_in_arrays => flag,target => v) AS stripped FROM jbs_stored`,
		`CREATE VIEW jbs_default AS SELECT id,jsonb_strip_nulls(v) AS stripped FROM jbs_stored`,
		`CREATE VIEW jbs_false AS SELECT id,jsonb_strip_nulls(target => v,strip_in_arrays => false) AS stripped FROM jbs_stored`,
		`CREATE VIEW jbs_null AS SELECT id,jsonb_strip_nulls(strip_in_arrays => NULL,target => v) AS stripped FROM jbs_stored`,
		`CREATE VIEW jbs_variadic AS SELECT id,jsonb_strip_nulls(target => v,VARIADIC strip_in_arrays => flag) AS stripped FROM jbs_stored`,
		`CREATE VIEW jbs_variadic_default AS SELECT id,jsonb_strip_nulls(VARIADIC v) AS stripped FROM jbs_stored`,
	} {
		jsonTypeRun(t, p, b, c, fixture.Exec(q, 0))
	}
	jsonTypeRun(t, p, b, c, fixture.Exec("INSERT INTO jbs_stored(id) VALUES(1)", 1))
	jsonTypeRun(t, p, b, c, fixture.Prepared(fixture.Exec("INSERT INTO jbs_stored VALUES(2,$1,true),(3,NULL,false)", 2, jsonbStripInput)))
	s := fixture.Select("SELECT id,stripped FROM jbs_named ORDER BY id", []string{"id", "stripped"}, []string{"INT4", "JSONB"}, [][]any{{int64(1), `{"a": [1]}`}, {int64(2), jsonbStripArrays}, {int64(3), nil}})
	jsonTypeBoth(t, p, b, c, s)
	for _, step := range jsonbStripStoredChecks() {
		jsonTypeBoth(t, p, b, c, step)
	}
	return s
}

func jsonbStripStoredChecks() []fixture.Step {
	return []fixture.Step{
		fixture.Select("SELECT stripped FROM jbs_default ORDER BY id", []string{"stripped"}, []string{"JSONB"}, [][]any{{`{"a": [null, 1]}`}, {jsonbStripDefault}, {nil}}),
		fixture.Select("SELECT stripped FROM jbs_false ORDER BY id", []string{"stripped"}, []string{"JSONB"}, [][]any{{`{"a": [null, 1]}`}, {jsonbStripDefault}, {nil}}),
		fixture.Select("SELECT stripped FROM jbs_null ORDER BY id", []string{"stripped"}, []string{"JSONB"}, [][]any{{nil}, {nil}, {nil}}),
		fixture.Select("SELECT stripped FROM jbs_variadic ORDER BY id", []string{"stripped"}, []string{"JSONB"}, [][]any{{`{"a": [1]}`}, {jsonbStripArrays}, {nil}}),
		fixture.Select("SELECT stripped FROM jbs_variadic_default ORDER BY id", []string{"stripped"}, []string{"JSONB"}, [][]any{{`{"a": [null, 1]}`}, {jsonbStripDefault}, {nil}}),
	}
}

func jsonbStripControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, check fixture.Step) {
	t.Helper()
	if b.PG {
		return
	} // PostgreSQL has no native control API.
	var baseline bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &baseline); err != nil {
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
	jsonTypeRun(t, p, b, c, fixture.Exec("CREATE TABLE jbs_replacement(v int)", 0))
	jsonTypeRun(t, p, b, c, fixture.Exec("INSERT INTO jbs_replacement VALUES(99)", 1))
	jsonTypeRun(t, p, b, c, fixture.Select("SELECT v FROM jbs_replacement", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}}))
	fixture.StringCoreRetainedRows(t, old, nil, check.Columns, check.Types, check.Rows, false)
	if len(metadata) != 2 || metadata[1].Name() != "stripped" || metadata[1].DatabaseTypeName() != "JSONB" {
		t.Fatal(metadata)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(baseline.Bytes())); err != nil {
		t.Fatal(err)
	}
	unchanged := func() {
		if len(metadata) != 2 || metadata[1].Name() != "stripped" || metadata[1].DatabaseTypeName() != "JSONB" {
			t.Fatal("retained metadata changed after Restore", metadata)
		}
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
			t.Fatal(err)
		}
		if after.String() != baseline.String() {
			t.Fatalf("persistent dump changed\nbefore:\n%s\nafter:\n%s", baseline.String(), after.String())
		}
		jsonTypeBoth(t, p, b, c, check)
		for _, step := range jsonbStripStoredChecks() {
			jsonTypeBoth(t, p, b, c, step)
		}
	}
	unchanged()
	for _, expression := range []string{"jsonb_strip_nulls($1,true)", "jsonb_strip_nulls('{}',$1)", "jsonb_strip_nulls(strip_in_arrays => true,target => $1)", "jsonb_strip_nulls(strip_in_arrays => $1,target => '{}')"} {
		script := "/*é*/ CREATE TABLE jbs_bad(v jsonb); INSERT INTO jbs_bad VALUES(" + expression + ");"
		d := fixture.ControlsReferenceDiagnostic("42P02", "parameters are not allowed in restore")
		d.Position = fixture.Position(len([]rune(script[:strings.Index(script, "$1")])) + 1)
		fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), &d)
		unchanged()
	}
	script := `CREATE TABLE jbs_bad(v jsonb); INSERT INTO jbs_bad VALUES(jsonb_strip_nulls('{"ok":null}')); INSERT INTO jbs_bad VALUES(jsonb_strip_nulls('oops'));`
	d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type json")
	d.Detail, d.Context = `Token "oops" is invalid.`, "JSON data, line 1: oops"
	d.Position = fixture.Position(len([]rune(script[:strings.Index(script, "'oops'")])) + 1)
	fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), &d)
	unchanged()
	script = `CREATE TABLE jbs_bad(v jsonb CONSTRAINT jbs_bad_clean CHECK(jsonb_strip_nulls(v)::text <> '"bad"')); INSERT INTO jbs_bad VALUES(jsonb_strip_nulls('"bad"'));`
	d = fixture.ControlsReferenceDiagnostic("23514", `new row for relation "jbs_bad" violates check constraint "jbs_bad_clean"`)
	d.Detail = `Failing row contains ("bad").`
	d.Schema, d.Table, d.Constraint = "public", "jbs_bad", "jbs_bad_clean"
	fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), &d)
	unchanged()
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	fixture.LexicalOrdinary(t, stuntgres.Restore(ctx, p.Native, bytes.NewReader(baseline.Bytes())), context.Canceled)
	unchanged()
	jsonbStripFreshWrites(t, p, b, c)
	unchanged()
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, baseline.String()); err != nil {
			t.Fatal("actual emitted SQL replay", err)
		}
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, check)
		for _, step := range jsonbStripStoredChecks() {
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, step)
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Prepared(step))
		}
		pg := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
		jsonbStripFreshWrites(t, p, pg, p.Postgres)
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferenceJSONBStripNullsStoredControls(t *testing.T) {
	jsonbStripScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := jsonbStripStored(t, p, b, c)
		jsonbStripFreshWrites(t, p, b, c)
		jsonbStripControls(t, p, b, c, check)
	})
}

func jsonbStripCheckFailure(query, row string, args ...any) fixture.Step {
	d := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "jbs_stored" violates check constraint "jbs_clean"`)
	d.Detail = "Failing row contains (" + row + ")."
	d.Schema, d.Table, d.Constraint = "public", "jbs_stored", "jbs_clean"
	s := fixture.ErrorDiagnostic(query, d, args...)
	s.ErrorStage = "execute"
	return s
}

func jsonbStripFreshWrites(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	for _, q := range []string{
		`INSERT INTO jbs_stored VALUES(10,jsonb_strip_nulls('{"a":null,"b":[null,1]}'),true)`,
		`INSERT INTO jbs_stored VALUES(11,jsonb_strip_nulls(strip_in_arrays => true,target => '{"a":null,"b":[null,1]}'),false)`,
		`INSERT INTO jbs_stored VALUES(12,jsonb_strip_nulls(target => '{"a":null,"b":[null,1]}',strip_in_arrays => false),true)`,
	} {
		jsonTypeRun(t, p, b, c, fixture.Exec(q, 1))
	}
	jsonTypeBoth(t, p, b, c, fixture.Select("SELECT v FROM jbs_stored WHERE id>=10 ORDER BY id", []string{"v"}, []string{"JSONB"}, [][]any{{`{"b": [null, 1]}`}, {`{"b": [1]}`}, {`{"b": [null, 1]}`}}))
	jsonTypeRun(t, p, b, c, fixture.Exec("DELETE FROM jbs_stored WHERE id>=10", 3))
	jsonTypeRun(t, p, b, c, fixture.Exec("INSERT INTO jbs_stored(id) VALUES(10)", 1))
	jsonTypeBoth(t, p, b, c, jsonbStripSelect("SELECT jsonb_strip_nulls(v,flag) AS stripped FROM jbs_stored WHERE id=10", `{"a": [1]}`))
	jsonTypeRun(t, p, b, c, fixture.Exec("DELETE FROM jbs_stored WHERE id=10", 1))
	jsonTypeBoth(t, p, b, c, jsonbStripCheckFailure(`INSERT INTO jbs_stored VALUES(10,'"bad"',true)`, `10, "bad", t`))
}

func TestReferenceJSONBStripNullsAtomicity(t *testing.T) {
	jsonbStripScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := jsonbStripStored(t, p, b, c)
		for _, s := range []fixture.Step{
			jsonbStripCheckFailure(`INSERT INTO jbs_stored VALUES(4,'{"ok":null}',true),(5,'"bad"',true)`, `5, "bad", t`),
			jsonbStripCheckFailure(`UPDATE jbs_stored SET v=CASE WHEN id=2 THEN '"bad"'::jsonb ELSE jsonb_strip_nulls(v,true) END`, `2, "bad", t`),
		} {
			jsonTypeBoth(t, p, b, c, s)
			jsonTypeBoth(t, p, b, c, check)
			jsonTypeBoth(t, p, b, c, fixture.Select("SELECT id,v FROM jbs_stored ORDER BY id", []string{"id", "v"}, []string{"INT4", "JSONB"}, [][]any{{int64(1), `{"a": [null, 1]}`}, {int64(2), `{"a": 1, "b": null, "c": [2, null, 3], "d": {"e": 4, "f": null}}`}, {int64(3), nil}}))
		}
		const q = "INSERT INTO jbs_stored VALUES(4,jsonb_strip_nulls($1,true),true),(5,jsonb_strip_nulls($2,true),true)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		s := jsonTypeError(q, "22P02", "invalid input syntax for type json", 0, `{"ok":null}`, "oops")
		s.SelectRows = false
		s.Diagnostic.Detail = `Token "oops" is invalid.`
		s.Diagnostic.Context = "JSON data, line 1: oops\nunnamed portal parameter $2 = '...'"
		jsonTypeRun(t, p, b, held, s)
		jsonTypeBoth(t, p, b, c, check)
		jsonTypeRun(t, p, b, held, fixture.Exec(q, 2, `{"ok":null}`, `[null,1]`))
		jsonTypeBoth(t, p, b, c, fixture.Select("SELECT id,v FROM jbs_stored WHERE id>=4 ORDER BY id", []string{"id", "v"}, []string{"INT4", "JSONB"}, [][]any{{int64(4), "{}"}, {int64(5), "[1]"}}))
		jsonTypeRun(t, p, b, c, fixture.Exec("DELETE FROM jbs_stored WHERE id>=4", 2))
		jsonbStripFreshWrites(t, p, b, c)
	})
}
