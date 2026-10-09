package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceBooleanDiagnosticsControls(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE b(id INT,flag BOOL)", 0))
	p.step(t, referenceExec("INSERT INTO b VALUES (1,TRUE),(2,FALSE),(3,NULL),(99,TRUE),(100,FALSE),(101,NULL)", 6))
	p.step(t, referenceExec("CREATE TABLE accepted(flag BOOL)", 0))
	for _, value := range []string{"t", "tr", "tru", "true", "y", "ye", "yes", "on", "1", "TrUe", " YES ", "\tOn\n", "f", "fa", "fal", "fals", "false", "n", "no", "of", "off", "0", "FaLsE", " NO ", "\tOfF\n"} {
		p.step(t, referencePrepared(referenceExec("INSERT INTO accepted VALUES ($1)", 1, []byte(value))))
	}
	p.step(t, referenceExec("CREATE TABLE repeated(flag BOOL)", 0))
	p.step(t, referenceExec("INSERT INTO repeated VALUES (TRUE),(FALSE),(NULL),(TRUE),(FALSE),(NULL),(TRUE),(FALSE)", 8))
	p.step(t, referenceExec("CREATE TABLE precedence(a BOOL,b BOOL)", 0))
	s := referenceSelect("SELECT id,flag FROM b ORDER BY id", []string{"id", "flag"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), false}, {int64(3), nil}, {int64(99), true}, {int64(100), false}, {int64(101), nil}})
	cursor, err := p.native.QueryContext(p.ctx, s.query)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
	bad := "CREATE TABLE replacement(flag BOOL); INSERT INTO replacement VALUES (TRUE),('o');"
	want := controlsReferenceDiagnostic("22P02", `invalid input syntax for type boolean: "o"`)
	want.Position = "77"
	booleanWrappedDiagnostic(t, stuntgres.Restore(p.ctx, p.native, strings.NewReader(bad)), want)
	if p.postgres != nil {
		step := referenceErrorDiagnostic(bad, want)
		step.errorStage = "execute"
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, step)
	}
	p.step(t, s)
	for _, backend := range p.backends() {
		step := referenceError("SELECT flag FROM replacement", "42P01")
		step.selectRows = true
		referenceRunStep(t, p.ctx, backend.db, backend.recorder, step)
		meta := referenceSelect("SELECT flag FROM b ORDER BY id", []string{"flag"}, []string{"BOOL"}, [][]any{{true}, {false}, {nil}, {true}, {false}, {nil}})
		rows, err := backend.db.QueryContext(p.ctx, meta.query)
		if err != nil {
			t.Fatal(err)
		}
		lexicalRows(t, backend.pg, rows, meta, nil)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if p.postgres != nil {
		if _, err := p.postgres.ExecContext(p.ctx, "DROP TABLE b,accepted,repeated,precedence"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
			t.Fatal(err)
		}
	}
	p.step(t, s)
	p.step(t, referenceSelect("SELECT flag FROM repeated ORDER BY flag", []string{"flag"}, []string{"BOOL"}, [][]any{{false}, {false}, {false}, {true}, {true}, {true}, {nil}, {nil}}))
	p.step(t, referenceSelect("SELECT a,b FROM precedence", []string{"a", "b"}, []string{"BOOL", "BOOL"}, nil))
	var accepted [][]any
	for range 12 {
		accepted = append(accepted, []any{true})
	}
	for range 13 {
		accepted = append(accepted, []any{false})
	}
	acceptedStep := referenceSelect("SELECT flag FROM accepted", []string{"flag"}, []string{"BOOL"}, accepted)
	acceptedStep.bag = true
	p.step(t, acceptedStep)
	var again bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &again); err != nil || !bytes.Equal(dump.Bytes(), again.Bytes()) {
		t.Fatalf("boolean redump changed: %v", err)
	}
	lexicalRows(t, false, cursor, s, nil)
	for _, backend := range p.backends() {
		_, err := backend.db.QueryContext(p.ctx, "SELECT id FROM b WHERE flag='o'")
		want := controlsReferenceDiagnostic("22P02", `invalid input syntax for type boolean: "o"`)
		want.Position = "29"
		booleanWrappedDiagnostic(t, err, want)
	}
}
