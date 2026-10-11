package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBooleanDiagnosticsSourceControls(t *testing.T) {
	p := fixture.NewScenario(t)
	step := func(s fixture.Step) { s.NonContext = true; p.Step(t, s) }
	step(fixture.Exec("CREATE TABLE b(id INT,flag BOOL)", 0))
	step(fixture.Exec("INSERT INTO b VALUES (99,TRUE),(100,FALSE),(101,NULL)", 3))
	s := fixture.Select("SELECT id,flag FROM b ORDER BY id", []string{"id", "flag"}, []string{"INT4", "BOOL"}, [][]any{{int64(99), true}, {int64(100), false}, {int64(101), nil}})
	cursor, err := p.Native.Query(s.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
	bad := "CREATE TABLE replacement(flag BOOL); INSERT INTO replacement VALUES (TRUE),('o');"
	want := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type boolean: "o"`)
	want.Position = "77"
	booleanWrappedDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)), want)
	if p.Postgres != nil {
		step := fixture.ErrorDiagnostic(bad, want)
		step.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, step)
	}
	step(s)
	for _, backend := range p.Backends() {
		step := fixture.Error("SELECT flag FROM replacement", "42P01")
		step.NonContext = true
		step.SelectRows = true
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step)
		meta := fixture.Select("SELECT flag FROM b", []string{"flag"}, []string{"BOOL"}, [][]any{{true}, {false}, {nil}})
		meta.Bag = true
		rows, err := backend.DB.Query(meta.Query)
		if err != nil {
			t.Fatal(err)
		}
		fixture.LexicalRows(t, backend.PG, rows, meta, nil)
	}
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
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP TABLE b"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal(err)
		}
	}
	step(s)
	for _, backend := range p.Backends() {
		meta := fixture.Select("SELECT flag FROM b", []string{"flag"}, []string{"BOOL"}, [][]any{{true}, {false}, {nil}})
		meta.Bag = true
		rows, err := backend.DB.Query(meta.Query)
		if err != nil {
			t.Fatal(err)
		}
		fixture.LexicalRows(t, backend.PG, rows, meta, nil)
	}
	var again bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(dump.Bytes(), again.Bytes()) {
		t.Fatalf("boolean redump changed: %v", err)
	}
	fixture.LexicalRows(t, false, cursor, s, nil)
	for _, backend := range p.Backends() {
		rows, err := backend.DB.Query("SELECT id FROM b WHERE flag='o'")
		if rows != nil {
			rows.Close()
			t.Fatal("failed Query exposed rows")
		}
		want := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type boolean: "o"`)
		want.Position = "29"
		booleanWrappedDiagnostic(t, err, want)
	}
}
