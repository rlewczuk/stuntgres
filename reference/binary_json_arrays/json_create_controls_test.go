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

func jsonCreateControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, check fixture.Step) {
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
	retained, err := c.QueryContext(p.Ctx, check.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	meta, err := old.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	owned, err := c.PrepareContext(p.Ctx, `SELECT to_json($1::jc_store_pair) AS v`)
	if err != nil {
		t.Fatal(err)
	}
	defer owned.Close()
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE TABLE jc_replacement(v int)`, 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO jc_replacement VALUES(99)`, 1))
	fixture.StringCoreRetainedRows(t, old, nil, check.Columns, check.Types, check.Rows, false)
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	fixture.StringCoreRetainedRows(t, retained, nil, check.Columns, check.Types, check.Rows, false)
	for i, ct := range meta {
		if ct.DatabaseTypeName() != check.Types[i] {
			t.Fatal(ct)
		}
	}
	rows, err := owned.QueryContext(p.Ctx, "(2,y)")
	if rows != nil {
		rows.Close()
		t.Fatal("replaced catalog published rows")
	}
	fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cached plan must not change parameter type"})
	jsonObjectBoth(t, p, b, c, check)
	jsonObjectBoth(t, p, b, c, fixture.Select(`SELECT t,n,z,a FROM jc_literal_view`, []string{"t", "n", "z", "a"}, []string{"JSON", "JSON", "JSON", "JSON"}, [][]any{{`"x"`, `12.340`, nil, `[1, 2]`}}))
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
		t.Fatal(err)
	}
	if redump.String() != dump.String() {
		t.Fatalf("redump changed\n%s\n%s", dump.String(), redump.String())
	}
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("actual emitted SQL replay", err)
		}
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, check)
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Select(`SELECT t,n,z,a FROM jc_literal_view`, []string{"t", "n", "z", "a"}, []string{"JSON", "JSON", "JSON", "JSON"}, [][]any{{`"x"`, `12.340`, nil, `[1, 2]`}}))
		if _, err := p.Postgres.ExecContext(p.Ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		script string
		want   fixture.Diagnostic
	}{
		{`CREATE TABLE jc_bad(v json); INSERT INTO jc_bad VALUES(json_build_array(1)); INSERT INTO jc_bad VALUES(json_build_object(NULL,1));`, fixture.ControlsReferenceDiagnostic("22004", "null value not allowed for object key")},
		{`CREATE TABLE jc_bad(v jsonb); INSERT INTO jc_bad VALUES(jsonb_build_array(1)); INSERT INTO jc_bad VALUES(jsonb_build_object(NULL,1));`, fixture.ControlsReferenceDiagnostic("22023", "argument 1: key must not be null")},
	} {
		fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(tc.script)), &tc.want)
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
			t.Fatal(err)
		}
		if after.String() != dump.String() {
			t.Fatal("failed staged Restore changed catalog/data")
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, check)
	}
	script := `/*é*/ CREATE TABLE jc_bad(v json); INSERT INTO jc_bad VALUES(to_json($1::int));`
	d := fixture.ControlsReferenceDiagnostic("42P02", "parameters are not allowed in restore")
	d.Position = fixture.Position(len([]rune(`/*é*/ CREATE TABLE jc_bad(v json); INSERT INTO jc_bad VALUES(to_json(`)) + 1)
	fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), &d)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, check)
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	rows, err = c.QueryContext(ctx, `SELECT json_build_array(1)`)
	if rows != nil {
		rows.Close()
		t.Fatal("precanceled rows")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	jsonObjectBoth(t, p, b, c, check)
}

func TestReferenceJSONCreateControls(t *testing.T) {
	jsonCreateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := jsonCreateStored(t, p, b, c)
		jsonCreateControls(t, p, b, c, check)
	})
}
