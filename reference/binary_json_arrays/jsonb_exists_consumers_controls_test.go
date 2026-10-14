package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonbExistsStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) fixture.Step {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec(`CREATE TABLE jbe_stored(id int,doc jsonb,d bool DEFAULT ('{"a":1}'::jsonb ? 'a'),g bool GENERATED ALWAYS AS (doc ? 'a') STORED)`, 0))
	run(fixture.Exec(`CREATE INDEX jbe_idx ON jbe_stored ((doc ? 'a'))`, 0))
	run(fixture.Exec(`CREATE VIEW jbe_view AS SELECT id,doc ? 'a' AS one,doc ?| '{missing,a}'::text[] AS any_key,doc ?& '{a,missing}'::text[] AS all_keys FROM jbe_stored`, 0))
	run(fixture.Exec(`INSERT INTO jbe_stored(id,doc) VALUES(1,'{"a":null}'),(2,'{"outer":{"a":1}}'),(3,'["a"]'),(4,'"a"'),(5,NULL)`, 5))
	check := fixture.Select("SELECT id,one,any_key,all_keys FROM jbe_view ORDER BY id", []string{"id", "one", "any_key", "all_keys"}, []string{"INT4", "BOOL", "BOOL", "BOOL"}, [][]any{{int64(1), true, true, false}, {int64(2), false, false, false}, {int64(3), true, true, false}, {int64(4), true, true, false}, {int64(5), nil, nil, nil}})
	jsonbExistsBoth(t, p, b, c, check)
	return check
}

func jsonbExistsControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, check fixture.Step) {
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
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE jbe_sentinel(id int)", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO jbe_sentinel VALUES(99)", 1))
	fixture.StringCoreRetainedRows(t, old, nil, check.Columns, check.Types, check.Rows, false)
	for i, ct := range meta {
		if ct.DatabaseTypeName() != check.Types[i] {
			t.Fatal(ct)
		}
	}
	var again bytes.Buffer
	for cycle := 1; cycle <= 3; cycle++ {
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatalf("restore cycle %d: %v", cycle, err)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, check)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT d,g FROM jbe_stored WHERE id=1", []string{"d", "g"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
		again.Reset()
		if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil {
			t.Fatal(err)
		}
		if dump.String() != again.String() {
			t.Fatalf("cycle %d changed canonical symbolic operator dump:\nbefore:\n%s\nafter:\n%s", cycle, dump.String(), again.String())
		}
	}
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("actual emitted SQL replay", err)
		}
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, check)
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Select("SELECT d,g FROM jbe_stored WHERE id=1", []string{"d", "g"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
	bad := `CREATE TABLE jbe_bad(doc jsonb,g bool GENERATED ALWAYS AS (doc ? 'a') STORED); INSERT INTO jbe_bad(doc) VALUES('{"a":1}'); INSERT INTO jbe_bad(doc) VALUES('bad');`
	d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type json")
	d.Detail, d.Context = `Token "bad" is invalid.`, "JSON data, line 1: bad"
	d.Position = fixture.Position(strings.Index(bad, "'bad'") + 1)
	fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)), &d)
	again.Reset()
	if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil {
		t.Fatal(err)
	}
	if again.String() != dump.String() {
		t.Fatal("failed Restore replaced complete catalog")
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, check)
}

func TestReferenceJSONBExistsConsumersControls(t *testing.T) {
	jsonbExistsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := jsonbExistsStored(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, tc := range []struct {
			predicate string
			rows      [][]any
		}{
			{`doc ? 'a'`, [][]any{{int64(1)}, {int64(3)}, {int64(4)}}},
			{`doc ?& '{}'::text[]`, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}}},
		} {
			jsonbExistsBoth(t, p, b, c, fixture.Select("SELECT id FROM jbe_stored WHERE "+tc.predicate+" ORDER BY id", []string{"id"}, []string{"INT4"}, tc.rows))
		}
		run(fixture.Select("SELECT id,d,g FROM jbe_stored ORDER BY id", []string{"id", "d", "g"}, []string{"INT4", "BOOL", "BOOL"}, [][]any{{int64(1), true, true}, {int64(2), true, false}, {int64(3), true, true}, {int64(4), true, true}, {int64(5), true, nil}}))
		run(fixture.Exec(`CREATE TABLE jbe_checked(id int,doc jsonb,CONSTRAINT jbe_has_a CHECK(doc ? 'a'))`, 0))
		run(fixture.Exec(`INSERT INTO jbe_checked VALUES(1,'{"a":1}'),(2,NULL)`, 2))
		d := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "jbe_checked" violates check constraint "jbe_has_a"`)
		d.Detail, d.Schema, d.Table, d.Constraint = "Failing row contains (3, {}).", "public", "jbe_checked", "jbe_has_a"
		run(fixture.ErrorDiagnostic(`INSERT INTO jbe_checked VALUES(3,'{}')`, d))
		q := "INSERT INTO jbe_stored(id,doc) VALUES(6,$1),(7,$2)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		d = fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type json")
		d.Detail, d.Context = `Token "bad" is invalid.`, "JSON data, line 1: bad\nunnamed portal parameter $2 = '...'"
		runBad := fixture.ErrorDiagnostic(q, d, `{"a":1}`, "bad")
		runBad.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, runBad)
		run(check)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 2, `{"a":1}`, `["a"]`))
		run(fixture.Exec("DELETE FROM jbe_stored WHERE id>5", 2))
		run(fixture.Exec(`UPDATE jbe_stored SET doc='{"a":2}' WHERE id=2`, 1))
		run(fixture.Select("SELECT g FROM jbe_stored WHERE id=2", []string{"g"}, []string{"BOOL"}, [][]any{{true}}))
		run(fixture.Exec(`UPDATE jbe_stored SET doc='{"outer":{"a":1}}' WHERE id=2`, 1))
		jsonbExistsControls(t, p, b, c, check)
	})
}
