package reference_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStringCoreLifecycle(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE string_core_source(id INT,raw CHAR(8),form TEXT)", 0),
		fixture.Exec("INSERT INTO string_core_source VALUES(1,' é ','NFC'),(2,' x ','NFC'),(3,NULL,'NFC')", 3),
		fixture.Exec(`CREATE TABLE string_core_dest(id INT,v TEXT DEFAULT normalize(trim(' é ')),CHECK (v IS NORMALIZED))`, 0),
		fixture.Exec("INSERT INTO string_core_dest(id) VALUES(0)", 1),
		fixture.Exec(`INSERT INTO string_core_dest SELECT id,"normalize"(btrim(raw),form)||'!' FROM string_core_source`, 3),
		fixture.Exec("CREATE TABLE string_core_prepared_dest(id INT,v TEXT)", 0),
		fixture.Prepared(fixture.Exec(`INSERT INTO string_core_prepared_dest SELECT id,"normalize"(btrim(raw),form)||'!' FROM string_core_source`, 3)),
		fixture.Select("SELECT id,v FROM string_core_dest ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(0), "é"}, {int64(1), "é!"}, {int64(2), "x!"}, {int64(3), nil}}),
		fixture.Exec(`CREATE VIEW string_core_view AS SELECT id,normalize(btrim(raw))||'!' AS v FROM string_core_source`, 0),
		fixture.Select("SELECT id,v FROM string_core_view ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "é!"}, {int64(2), "x!"}, {int64(3), nil}}),
		fixture.Select("SELECT s.id,d.v FROM string_core_source s JOIN string_core_dest d ON normalize(btrim(s.raw))||'!'=d.v ORDER BY s.id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "é!"}, {int64(2), "x!"}}),
		fixture.Prepared(fixture.Select("SELECT s.id,d.v FROM string_core_source s JOIN string_core_dest d ON normalize(btrim(s.raw))||'!'=d.v ORDER BY s.id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "é!"}, {int64(2), "x!"}})),
		fixture.Select("SELECT btrim(raw) AS v,count(*) AS n FROM string_core_source GROUP BY btrim(raw) HAVING btrim(raw) IS NORMALIZED ORDER BY v", []string{"v", "n"}, []string{"TEXT", "INT8"}, [][]any{{"x", int64(1)}}),
		fixture.Select("UPDATE string_core_dest SET v=normalize(v||'a') WHERE id=2 RETURNING id,v", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "x!a"}}),
		fixture.Select("DELETE FROM string_core_dest WHERE btrim(v)='x!a' RETURNING id,v", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "x!a"}}),
		fixture.Exec("UPDATE string_core_source SET form='bad' WHERE id=2", 1),
		fixture.ImplicitError(`INSERT INTO string_core_dest SELECT id+10,"normalize"(raw,form) FROM string_core_source`, fixture.Diagnostic{Code: "22023", Message: "invalid normalization form: bad"}),
		fixture.Select("SELECT id FROM string_core_dest ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(1)}, {int64(3)}}),
		fixture.Exec("UPDATE string_core_source SET form='NFC' WHERE id=2", 1),
	} {
		p.Step(t, s)
	}
	p.Step(t, fixture.Select("SELECT id,v FROM string_core_view WHERE v IS NORMALIZED ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "é!"}, {int64(2), "x!"}}))
	p.Step(t, fixture.Error("INSERT INTO string_core_dest VALUES(20,'é')", "23514"))
	raw, err := p.NativeConnector.Connect(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	held, err := raw.(driver.QueryerContext).QueryContext(p.Ctx, "SELECT id,v FROM string_core_view ORDER BY id", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	p.Step(t, fixture.Select("SELECT id,v FROM string_core_view ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "é!"}, {int64(2), "x!"}, {int64(3), nil}}))
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
		t.Fatal("redump mismatch", err)
	}
	values := make([]driver.Value, 2)
	for _, row := range [][]any{{int64(1), "é!"}, {int64(2), "x!"}, {int64(3), nil}} {
		if err := held.Next(values); err != nil || !reflect.DeepEqual([]any{values[0], values[1]}, row) {
			t.Fatal("retained raw snapshot", values, row, err)
		}
	}
	if err := held.Next(values); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(`CREATE TABLE staged(v TEXT); INSERT INTO staged VALUES("normalize"('x','bad'))`))
	fixture.StringCoreControlDiagnostic(t, err, fixture.Diagnostic{Code: "22023", Message: "invalid normalization form: bad"})
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relname='staged'", []string{"relname"}, []string{"NAME"}, nil))
	var after bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(dump.Bytes(), after.Bytes()) {
		t.Fatal("failed Restore changed publication", err)
	}
	// Native dump is a SQL deliverable: replay it into the real oracle after clearing its schema.
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("PostgreSQL dump replay", err)
		}
	}
	p.Step(t, fixture.Prepared(fixture.Select("SELECT id,v FROM string_core_view ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "é!"}, {int64(2), "x!"}, {int64(3), nil}})))
	p.Step(t, fixture.Exec("INSERT INTO string_core_dest(id) VALUES(21)", 1))
	p.Step(t, fixture.Select("SELECT v FROM string_core_dest WHERE id=21", []string{"v"}, []string{"TEXT"}, [][]any{{"é"}}))
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	for _, backend := range p.Backends() {
		db := backend.DB
		rows, err := db.QueryContext(ctx, "SELECT normalize('x')")
		if rows != nil {
			rows.Close()
			t.Fatal("cancel exposed rows")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
}
