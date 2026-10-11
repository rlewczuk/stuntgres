package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func formatReferenceControlPhase(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, want fixture.Step) {
	t.Helper()
	if b.PG {
		return
	} // These public controls have no PostgreSQL API equivalent.
	var before bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
		t.Fatal(err)
	}
	old, err := c.QueryContext(p.Ctx, want.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	metadata, err := old.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	bad := `CREATE TABLE format_failed(v text); INSERT INTO format_failed VALUES(format('%s','first')); INSERT INTO format_failed VALUES(format('%I',NULL));`
	d := fixture.ControlsReferenceDiagnostic("22004", "null values cannot be formatted as an SQL identifier")
	fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)), &d)
	for _, input := range []string{
		`CREATE TABLE format_failed(v text); INSERT INTO format_failed VALUES(format('%s',format('%s',$1)));`,
		`CREATE TABLE format_failed(v text); INSERT INTO format_failed VALUES(format('%s',VARIADIC $1::name[]));`,
	} {
		d := fixture.ControlsReferenceDiagnostic("42P02", "parameters are not allowed in restore")
		d.Position = fixture.Position(len([]rune(input[:strings.Index(input, "$1")])) + 1)
		fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(input)), &d)
	}
	var after bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
		t.Fatal(err)
	}
	if before.String() != after.String() {
		t.Fatal("failed Restore changed catalog/data")
	}
	for i, typ := range want.Types {
		if metadata[i].DatabaseTypeName() != typ {
			t.Fatalf("retained column %d type %s, want %s", i, metadata[i].DatabaseTypeName(), typ)
		}
	}
	fixture.StringCoreRetainedRows(t, old, nil, want.Columns, want.Types, want.Rows, want.Bag)
	formatReferenceBoth(t, p, b, c, want)
	retained, err := c.QueryContext(p.Ctx, want.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(before.Bytes())); err != nil {
		t.Fatal(err)
	}
	fixture.StringCoreRetainedRows(t, retained, nil, want.Columns, want.Types, want.Rows, want.Bag)
	formatReferenceBoth(t, p, b, c, want)
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
		t.Fatal(err)
	}
	if before.String() != redump.String() {
		t.Fatal("format canonical redump changed", before.String(), redump.String())
	}
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, redump.String()); err != nil {
			t.Fatal("PostgreSQL replay of native format SQL", err)
		}
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, want)
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferenceFormatControls(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE DOMAIN format_control_names AS name[]", 0),
			fixture.Exec("CREATE TABLE format_control(id int,v text DEFAULT (format('%s:%s','x'::char(3),5.00::numeric)),a format_control_names DEFAULT '{a,b}',CHECK(format('%s',v)<>'bad'))", 0),
			fixture.Exec("INSERT INTO format_control(id) VALUES(1)", 1),
			fixture.Exec("INSERT INTO format_control VALUES(2,format('%I','select'),'{c,d}')", 1),
			fixture.Exec("CREATE VIEW format_control_view AS SELECT id,format('%s',v) AS v,format('%2$s/%1$s',VARIADIC a) AS names FROM format_control", 0),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		want := fixture.Select("SELECT id,v,names FROM format_control_view ORDER BY id", []string{"id", "v", "names"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "x  :5.00", "b/a"}, {int64(2), `"select"`, "d/c"}})
		formatReferenceBoth(t, p, b, c, want)
		formatReferenceControlPhase(t, p, b, c, want)
		if b.PG {
			return
		}
		input := `CREATE TABLE format_replayed(v text); INSERT INTO format_replayed VALUES(format('%s/%L',true,NULL)),(format('%2$s/%1$s',VARIADIC '{a,b}'::name[]));`
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(input)); err != nil {
			t.Fatal(err)
		}
		formatReferenceBoth(t, p, b, c, fixture.Select("SELECT v FROM format_replayed ORDER BY v", []string{"v"}, []string{"TEXT"}, [][]any{{"b/a"}, {"t/NULL"}}))
	})
}
