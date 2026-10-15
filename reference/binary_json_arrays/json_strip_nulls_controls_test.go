package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonStripStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) fixture.Step {
	t.Helper()
	for _, q := range []string{
		`CREATE TABLE js_stored(id int,v json DEFAULT json_strip_nulls('{"x":null,"a":[null,1]}'),flag bool DEFAULT true,CONSTRAINT js_clean CHECK (json_strip_nulls(v,flag)::text <> '"bad"'))`,
		`CREATE VIEW js_named AS SELECT id,json_strip_nulls(strip_in_arrays => flag,target => v) AS stripped FROM js_stored`,
		`CREATE VIEW js_default AS SELECT id,json_strip_nulls(v) AS stripped FROM js_stored`,
		`CREATE VIEW js_false AS SELECT id,json_strip_nulls(target => v,strip_in_arrays => false) AS stripped FROM js_stored`,
		`CREATE VIEW js_null AS SELECT id,json_strip_nulls(strip_in_arrays => NULL,target => v) AS stripped FROM js_stored`,
		`CREATE VIEW js_variadic AS SELECT id,json_strip_nulls(target => v,VARIADIC strip_in_arrays => flag) AS stripped FROM js_stored`,
		`CREATE VIEW js_variadic_default AS SELECT id,json_strip_nulls(VARIADIC v) AS stripped FROM js_stored`,
	} {
		jsonTypeRun(t, p, b, c, fixture.Exec(q, 0))
	}
	jsonTypeRun(t, p, b, c, fixture.Exec("INSERT INTO js_stored(id) VALUES(1)", 1))
	jsonTypeRun(t, p, b, c, fixture.Prepared(fixture.Exec("INSERT INTO js_stored VALUES(2,$1,true),(3,NULL,false)", 2, jsonStripInput)))
	s := fixture.Select("SELECT id,stripped FROM js_named ORDER BY id", []string{"id", "stripped"}, []string{"INT4", "JSON"}, [][]any{{int64(1), `{"a":[1]}`}, {int64(2), jsonStripArrays}, {int64(3), nil}})
	jsonTypeBoth(t, p, b, c, s)
	for _, step := range jsonStripStoredChecks() {
		jsonTypeBoth(t, p, b, c, step)
	}
	return s
}

func jsonStripStoredChecks() []fixture.Step {
	return []fixture.Step{
		fixture.Select("SELECT stripped FROM js_default ORDER BY id", []string{"stripped"}, []string{"JSON"}, [][]any{{`{"a":[null,1]}`}, {jsonStripDefault}, {nil}}),
		fixture.Select("SELECT stripped FROM js_false ORDER BY id", []string{"stripped"}, []string{"JSON"}, [][]any{{`{"a":[null,1]}`}, {jsonStripDefault}, {nil}}),
		fixture.Select("SELECT stripped FROM js_null ORDER BY id", []string{"stripped"}, []string{"JSON"}, [][]any{{nil}, {nil}, {nil}}),
		fixture.Select("SELECT stripped FROM js_variadic ORDER BY id", []string{"stripped"}, []string{"JSON"}, [][]any{{`{"a":[1]}`}, {jsonStripArrays}, {nil}}),
		fixture.Select("SELECT stripped FROM js_variadic_default ORDER BY id", []string{"stripped"}, []string{"JSON"}, [][]any{{`{"a":[null,1]}`}, {jsonStripDefault}, {nil}}),
	}
}

func jsonStripControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, check fixture.Step) {
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
	jsonTypeRun(t, p, b, c, fixture.Exec("CREATE TABLE js_replacement(v int)", 0))
	jsonTypeRun(t, p, b, c, fixture.Exec("INSERT INTO js_replacement VALUES(99)", 1))
	jsonTypeRun(t, p, b, c, fixture.Select("SELECT v FROM js_replacement", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}}))
	fixture.StringCoreRetainedRows(t, old, nil, check.Columns, check.Types, check.Rows, false)
	if len(metadata) != 2 || metadata[1].Name() != "stripped" || metadata[1].DatabaseTypeName() != "JSON" {
		t.Fatal(metadata)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(baseline.Bytes())); err != nil {
		t.Fatal(err)
	}
	unchanged := func() {
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
			t.Fatal(err)
		}
		if after.String() != baseline.String() {
			t.Fatalf("persistent dump changed\nbefore:\n%s\nafter:\n%s", baseline.String(), after.String())
		}
		jsonTypeBoth(t, p, b, c, check)
		for _, step := range jsonStripStoredChecks() {
			jsonTypeBoth(t, p, b, c, step)
		}
	}
	unchanged()
	for _, expression := range []string{"json_strip_nulls($1,true)", "json_strip_nulls('{}',$1)", "json_strip_nulls(strip_in_arrays => true,target => $1)", "json_strip_nulls(strip_in_arrays => $1,target => '{}')"} {
		script := "/*é*/ CREATE TABLE js_bad(v json); INSERT INTO js_bad VALUES(" + expression + ");"
		d := fixture.ControlsReferenceDiagnostic("42P02", "parameters are not allowed in restore")
		d.Position = fixture.Position(len([]rune(script[:strings.Index(script, "$1")])) + 1)
		fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), &d)
		unchanged()
	}
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, baseline.String()); err != nil {
			t.Fatal("actual emitted SQL replay", err)
		}
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, check)
		for _, step := range jsonStripStoredChecks() {
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, step)
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Prepared(step))
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferenceJSONStripNullsControls(t *testing.T) {
	jsonStripScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonStripControls(t, p, b, c, jsonStripStored(t, p, b, c))
	})
}

func TestReferenceJSONStripNullsAtomicity(t *testing.T) {
	jsonStripScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonTypeRun(t, p, b, c, fixture.Exec("CREATE TABLE js_atomic(id int,v json)", 0))
		jsonTypeRun(t, p, b, c, fixture.Exec(`INSERT INTO js_atomic VALUES(0,'{"sentinel":1}')`, 1))
		before := fixture.Select("SELECT id,v FROM js_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "JSON"}, [][]any{{int64(0), `{"sentinel":1}`}})
		for _, prepared := range []bool{false, true} {
			s := jsonStripProcessingError(`INSERT INTO js_atomic VALUES(1,json_strip_nulls('{"ok":null}')),(2,json_strip_nulls('{"x":"\u0000"}'))`, "", "22P05", `\u0000 cannot be converted to text.`, `JSON data, line 1: {"x":"\u0000...`, false)
			s.SelectRows = false
			if prepared {
				s = fixture.Prepared(s)
			}
			jsonTypeRun(t, p, b, c, s)
			jsonTypeRun(t, p, b, c, before)
		}
		jsonTypeRun(t, p, b, c, fixture.Exec(`INSERT INTO js_atomic VALUES(1,'{"ok":null}'),(2,'{"x":"\u0000"}')`, 2))
		update := jsonStripProcessingError("UPDATE js_atomic SET v=json_strip_nulls(v)", "", "22P05", `\u0000 cannot be converted to text.`, `JSON data, line 1: {"x":"\u0000...`, false)
		update.SelectRows = false
		jsonTypeBoth(t, p, b, c, update)
		jsonTypeBoth(t, p, b, c, fixture.Select("SELECT id,v FROM js_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "JSON"}, [][]any{{int64(0), `{"sentinel":1}`}, {int64(1), `{"ok":null}`}, {int64(2), `{"x":"\u0000"}`}}))
		jsonTypeRun(t, p, b, c, fixture.Exec("DELETE FROM js_atomic WHERE id>0", 2))
		jsonTypeRun(t, p, b, c, before)
	})
}
