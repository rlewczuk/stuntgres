package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func jsonbPrettyStoredCheck(pg bool) fixture.Step {
	s := fixture.Select("SELECT id,p,d FROM jbp_store ORDER BY id", []string{"id", "p", "d"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{
		{int64(1), jsonbPrettyEmptyNested, "[\n]"}, {int64(2), "null", "[\n]"}, {int64(3), nil, "[\n]"},
	})
	return jsonTypeWithMetadata(s, pg, jsonTypeSourceColumn{index: 0, nullable: true}, jsonTypeSourceColumn{index: 1, nullable: true}, jsonTypeSourceColumn{index: 2, nullable: true})
}

func jsonbPrettyStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { jsonTypeRun(t, p, b, c, s) }
	run(fixture.Exec(`CREATE TABLE jbp_store(id int,j jsonb DEFAULT '{"z":1,"a":2}',p text GENERATED ALWAYS AS (jsonb_pretty(j)) STORED,d text DEFAULT jsonb_pretty('[]'),CONSTRAINT jbp_valid CHECK(jsonb_pretty(j) <> '"bad"'))`, 0))
	run(fixture.Exec("CREATE INDEX jbp_index ON jbp_store((jsonb_pretty(j)))", 0))
	run(fixture.Exec("CREATE VIEW jbp_view AS SELECT id,jsonb_pretty(j) AS pretty FROM jbp_store", 0))
	run(fixture.Exec("INSERT INTO jbp_store(id) VALUES(1)", 1))
	run(fixture.Prepared(fixture.Exec("INSERT INTO jbp_store(id,j) VALUES(2,$1)", 1, "null")))
	run(fixture.Exec("INSERT INTO jbp_store(id,j) VALUES(3,NULL)", 1))
	jsonTypeBoth(t, p, b, c, fixture.Select("SELECT id,pretty FROM jbp_view ORDER BY id", []string{"id", "pretty"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "{\n    \"a\": 2,\n    \"z\": 1\n}"}, {int64(2), "null"}, {int64(3), nil}}))
	run(fixture.Prepared(fixture.Exec("UPDATE jbp_store SET j=$1 WHERE id=1", 1, "[[],{}]")))
	check := jsonbPrettyStoredCheck(b.PG)
	jsonTypeBoth(t, p, b, c, check)
	literal := `INSERT INTO jbp_store(id,j) VALUES(4,'{}'),(5,'oops')`
	s := jsonbPrettyInputError(literal, false)
	s.SelectRows = false
	jsonTypeAnalysisError(t, p, b, c, s)
	bound := `INSERT INTO jbp_store(id,j) VALUES(4,'{}'),(5,$1)`
	s = jsonbPrettyInputError(bound, true, "oops")
	s.SelectRows = false
	jsonTypeBoth(t, p, b, c, s)
	d := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "jbp_store" violates check constraint "jbp_valid"`)
	d.Detail = "Failing row contains (5, \"bad\", \"bad\", [\n])."
	d.Schema, d.Table, d.Constraint = "public", "jbp_store", "jbp_valid"
	s = fixture.ErrorDiagnostic(`INSERT INTO jbp_store(id,j) VALUES(4,'{}'),(5,'"bad"')`, d)
	s.ErrorStage = "execute"
	jsonTypeBoth(t, p, b, c, s)
	jsonTypeBoth(t, p, b, c, check)
	jsonTypeBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM jbp_store", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}))
	// A successful write after both failures proves the same physical owner lives.
	run(fixture.Exec("UPDATE jbp_store SET j='null' WHERE id=2", 1))
	jsonbPrettyControls(t, p, b, c, check)
}

func jsonbPrettyControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, check fixture.Step) {
	t.Helper()
	if b.PG {
		return
	} // Common SQL above also runs independently on PostgreSQL.
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	old, err := c.QueryContext(p.Ctx, check.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	jsonTypeMetadataCheck(t, old, false, check.Columns, check.Types, jsonTypeSourceColumn{index: 0, nullable: true}, jsonTypeSourceColumn{index: 1, nullable: true}, jsonTypeSourceColumn{index: 2, nullable: true})
	metadata, err := old.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	jsonTypeRun(t, p, b, c, fixture.Exec("CREATE TABLE jbp_replacement(v int)", 0))
	jsonTypeRun(t, p, b, c, fixture.Exec("INSERT INTO jbp_replacement VALUES(99)", 1))
	fixture.StringCoreRetainedRows(t, old, nil, check.Columns, check.Types, check.Rows, false)
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	for i, ct := range metadata {
		if ct.Name() != check.Columns[i] || ct.DatabaseTypeName() != check.Types[i] {
			t.Fatal("retained description changed", ct)
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
			t.Fatal("replacement changed dump")
		}
		jsonTypeBoth(t, p, b, c, check)
	}
	unchanged()
	script := `CREATE TABLE jbp_broken(v text); INSERT INTO jbp_broken VALUES('earlier'); INSERT INTO jbp_broken VALUES(jsonb_pretty('oops'));`
	d := jsonbPrettyInputError("SELECT jsonb_pretty('oops')", false).Diagnostic
	d.Position = fixture.Position(utf8.RuneCountInString(script[:strings.Index(script, "'oops'")]) + 1)
	fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), d)
	unchanged()
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	fixture.LexicalOrdinary(t, stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())), context.Canceled)
	unchanged()
	// Restored defaults, generated values, view and constraint/index recipes must
	// execute, rather than merely restoring the materialized saved values.
	jsonTypeRun(t, p, b, c, fixture.Exec("INSERT INTO jbp_store(id) VALUES(4)", 1))
	fresh := fixture.Select("SELECT pretty FROM jbp_view WHERE id=4", []string{"pretty"}, []string{"TEXT"}, [][]any{{"{\n    \"a\": 2,\n    \"z\": 1\n}"}})
	// This projects a declared view column, not a computed SELECT expression.
	jsonTypeBoth(t, p, b, c, jsonTypeWithMetadata(fresh, b.PG, jsonTypeSourceColumn{index: 0, nullable: true}))
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("actual emitted dump replay", err)
		}
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, jsonbPrettyStoredCheck(true))
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec("INSERT INTO jbp_store(id) VALUES(4)", 1))
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, jsonTypeWithMetadata(fresh, true))
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferenceJSONBPrettyStoredReplay(t *testing.T) { jsonTypeScenario(t, jsonbPrettyStored) }
