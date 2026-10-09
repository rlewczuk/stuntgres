package reference_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceUnicodeEscapesSourceControls(t *testing.T) {
	p := newReferenceScenario(t)
	step := func(s referenceStep) { s.nonContext = true; p.step(t, s) }
	nativeStep := func(s referenceStep) { s.nonContext = true; referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, s) }
	nativeStep(referenceExec(`CREATE TABLE U&"\0041"(id INT NOT NULL,U&"\+01F600" TEXT)`, 0))
	nativeStep(referenceExec(`INSERT INTO "A" VALUES (1,U&'\0041'),(2,U&'\D83D\DE00'),(3,U&''),(4,NULL),(5,U&'O''Brien\\\0001')`, 5))
	s := referenceSelect(`SELECT * FROM "A" ORDER BY id`, []string{"id", "😀"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "A"}, {int64(2), "😀"}, {int64(3), ""}, {int64(4), nil}, {int64(5), "O'Brien\\\x01"}})
	held, err := p.native.Query(s.query)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	for _, tc := range []struct{ script, position string }{
		{`CREATE TABLE staged(x TEXT); INSERT INTO staged VALUES ('valid'); INSERT INTO staged VALUES (U&'\061')`, "97"},
		{`CREATE TABLE staged(x TEXT); INSERT INTO staged VALUES ('valid'); INSERT INTO staged VALUES (U&'a''😀\061')`, "101"},
	} {
		err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(tc.script))
		want := controlsReferenceDiagnostic("42601", "invalid Unicode escape")
		want.Position, want.Hint = tc.position, `Unicode escapes must be \XXXX or \+XXXXXX.`
		referenceAssertDiagnostic(t, err, &want)
		nativeStep(s)
		missing := referenceError("SELECT * FROM staged", "42P01")
		missing.selectRows = true
		nativeStep(missing)
	}
	metadata := s
	metadata.query, metadata.bag = `SELECT * FROM "A"`, true
	rows, err := p.native.Query(metadata.query)
	if err != nil {
		t.Fatal(err)
	}
	lexicalRows(t, false, rows, metadata, []bool{false, true})
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "U&") || !strings.Contains(dump.String(), `"A"`) || !strings.Contains(dump.String(), `"😀"`) {
		t.Fatalf("canonical names %s", dump.String())
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if p.postgres != nil {
		if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
			t.Fatalf("exact native dump %v\n%s", err, dump.String())
		}
	}
	step(s)
	var again bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &again); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), again.Bytes()) {
		t.Fatal("redump changed")
	}
	lexicalRows(t, false, held, s, []bool{false, true})
	canceled, cancel := context.WithCancel(p.ctx)
	cancel()
	lexicalOrdinary(t, stuntgres.Dump(canceled, p.native, io.Discard), context.Canceled)
	lexicalOrdinary(t, stuntgres.Reset(canceled, p.native), context.Canceled)
	lexicalOrdinary(t, stuntgres.Restore(canceled, p.native, bytes.NewReader(dump.Bytes())), context.Canceled)
	reading, stop := context.WithCancel(p.ctx)
	defer stop()
	lexicalOrdinary(t, stuntgres.Restore(reading, p.native, &lexicalCancelReader{cancel: stop}), context.Canceled)
	failure := errors.New("unicode control I/O")
	lexicalOrdinary(t, stuntgres.Restore(p.ctx, p.native, lexicalFailIO{failure}), failure)
	lexicalOrdinary(t, stuntgres.Dump(p.ctx, p.native, lexicalFailIO{failure}), failure)
	step(s)
	if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(`CREATE TABLE U&"\0041"(id INT NOT NULL,U&"\+01F600" TEXT); INSERT INTO U&"\0041" VALUES (1,U&'\0041'),(2,U&'\D83D\DE00'),(3,U&''),(4,NULL),(5,U&'O''Brien\\\0001')`)); err != nil {
		t.Fatal(err)
	}
	step(s)
}
