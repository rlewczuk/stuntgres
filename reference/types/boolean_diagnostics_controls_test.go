package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBooleanDiagnosticsControls(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE b(id INT,flag BOOL)", 0))
	p.Step(t, fixture.Exec("INSERT INTO b VALUES (1,TRUE),(2,FALSE),(3,NULL),(99,TRUE),(100,FALSE),(101,NULL)", 6))
	p.Step(t, fixture.Exec("CREATE TABLE accepted(flag BOOL)", 0))
	for _, value := range []string{"t", "tr", "tru", "true", "y", "ye", "yes", "on", "1", "TrUe", " YES ", "\tOn\n", "f", "fa", "fal", "fals", "false", "n", "no", "of", "off", "0", "FaLsE", " NO ", "\tOfF\n"} {
		p.Step(t, fixture.Prepared(fixture.Exec("INSERT INTO accepted VALUES ($1)", 1, []byte(value))))
	}
	p.Step(t, fixture.Exec("CREATE TABLE repeated(flag BOOL)", 0))
	p.Step(t, fixture.Exec("INSERT INTO repeated VALUES (TRUE),(FALSE),(NULL),(TRUE),(FALSE),(NULL),(TRUE),(FALSE)", 8))
	p.Step(t, fixture.Exec("CREATE TABLE precedence(a BOOL,b BOOL)", 0))
	s := fixture.Select("SELECT id,flag FROM b ORDER BY id", []string{"id", "flag"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), false}, {int64(3), nil}, {int64(99), true}, {int64(100), false}, {int64(101), nil}})
	cursor, err := p.Native.QueryContext(p.Ctx, s.Query)
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
	p.Step(t, s)
	for _, backend := range p.Backends() {
		step := fixture.Error("SELECT flag FROM replacement", "42P01")
		step.SelectRows = true
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step)
		meta := fixture.Select("SELECT flag FROM b ORDER BY id", []string{"flag"}, []string{"BOOL"}, [][]any{{true}, {false}, {nil}, {true}, {false}, {nil}})
		rows, err := backend.DB.QueryContext(p.Ctx, meta.Query)
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
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP TABLE b,accepted,repeated,precedence"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal(err)
		}
	}
	p.Step(t, s)
	p.Step(t, fixture.Select("SELECT flag FROM repeated ORDER BY flag", []string{"flag"}, []string{"BOOL"}, [][]any{{false}, {false}, {false}, {true}, {true}, {true}, {nil}, {nil}}))
	p.Step(t, fixture.Select("SELECT a,b FROM precedence", []string{"a", "b"}, []string{"BOOL", "BOOL"}, nil))
	var accepted [][]any
	for range 12 {
		accepted = append(accepted, []any{true})
	}
	for range 13 {
		accepted = append(accepted, []any{false})
	}
	acceptedStep := fixture.Select("SELECT flag FROM accepted", []string{"flag"}, []string{"BOOL"}, accepted)
	acceptedStep.Bag = true
	p.Step(t, acceptedStep)
	var again bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(dump.Bytes(), again.Bytes()) {
		t.Fatalf("boolean redump changed: %v", err)
	}
	fixture.LexicalRows(t, false, cursor, s, nil)
	for _, backend := range p.Backends() {
		_, err := backend.DB.QueryContext(p.Ctx, "SELECT id FROM b WHERE flag='o'")
		want := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type boolean: "o"`)
		want.Position = "29"
		booleanWrappedDiagnostic(t, err, want)
	}
}
