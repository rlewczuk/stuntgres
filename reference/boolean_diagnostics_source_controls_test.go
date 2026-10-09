package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceBooleanDiagnosticsSourceControls(t *testing.T) {
	p := newReferenceScenario(t)
	step := func(s referenceStep) { s.nonContext = true; p.step(t, s) }
	step(referenceExec("CREATE TABLE b(id INT,flag BOOL)", 0))
	step(referenceExec("INSERT INTO b VALUES (99,TRUE),(100,FALSE),(101,NULL)", 3))
	s := referenceSelect("SELECT id,flag FROM b ORDER BY id", []string{"id", "flag"}, []string{"INT4", "BOOL"}, [][]any{{int64(99), true}, {int64(100), false}, {int64(101), nil}})
	cursor, err := p.native.Query(s.query)
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
	step(s)
	for _, backend := range p.backends() {
		step := referenceError("SELECT flag FROM replacement", "42P01")
		step.nonContext = true
		step.selectRows = true
		referenceRunStep(t, p.ctx, backend.db, backend.recorder, step)
		meta := referenceSelect("SELECT flag FROM b", []string{"flag"}, []string{"BOOL"}, [][]any{{true}, {false}, {nil}})
		meta.bag = true
		rows, err := backend.db.Query(meta.query)
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
		if _, err := p.postgres.ExecContext(p.ctx, "DROP TABLE b"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
			t.Fatal(err)
		}
	}
	step(s)
	for _, backend := range p.backends() {
		meta := referenceSelect("SELECT flag FROM b", []string{"flag"}, []string{"BOOL"}, [][]any{{true}, {false}, {nil}})
		meta.bag = true
		rows, err := backend.db.Query(meta.query)
		if err != nil {
			t.Fatal(err)
		}
		lexicalRows(t, backend.pg, rows, meta, nil)
	}
	var again bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &again); err != nil || !bytes.Equal(dump.Bytes(), again.Bytes()) {
		t.Fatalf("boolean redump changed: %v", err)
	}
	lexicalRows(t, false, cursor, s, nil)
	for _, backend := range p.backends() {
		rows, err := backend.db.Query("SELECT id FROM b WHERE flag='o'")
		if rows != nil {
			rows.Close()
			t.Fatal("failed Query exposed rows")
		}
		want := controlsReferenceDiagnostic("22P02", `invalid input syntax for type boolean: "o"`)
		want.Position = "29"
		booleanWrappedDiagnostic(t, err, want)
	}
}
