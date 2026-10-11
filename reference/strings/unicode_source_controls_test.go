package reference_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUnicodeEscapesSourceControls(t *testing.T) {
	p := fixture.NewScenario(t)
	step := func(s fixture.Step) { s.NonContext = true; p.Step(t, s) }
	nativeStep := func(s fixture.Step) { s.NonContext = true; fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, s) }
	nativeStep(fixture.Exec(`CREATE TABLE U&"\0041"(id INT NOT NULL,U&"\+01F600" TEXT)`, 0))
	nativeStep(fixture.Exec(`INSERT INTO "A" VALUES (1,U&'\0041'),(2,U&'\D83D\DE00'),(3,U&''),(4,NULL),(5,U&'O''Brien\\\0001')`, 5))
	s := fixture.Select(`SELECT * FROM "A" ORDER BY id`, []string{"id", "😀"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "A"}, {int64(2), "😀"}, {int64(3), ""}, {int64(4), nil}, {int64(5), "O'Brien\\\x01"}})
	held, err := p.Native.Query(s.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	for _, tc := range []struct{ script, position string }{
		{`CREATE TABLE staged(x TEXT); INSERT INTO staged VALUES ('valid'); INSERT INTO staged VALUES (U&'\061')`, "97"},
		{`CREATE TABLE staged(x TEXT); INSERT INTO staged VALUES ('valid'); INSERT INTO staged VALUES (U&'a''😀\061')`, "101"},
	} {
		err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(tc.script))
		want := fixture.ControlsReferenceDiagnostic("42601", "invalid Unicode escape")
		want.Position, want.Hint = tc.position, `Unicode escapes must be \XXXX or \+XXXXXX.`
		fixture.AssertDiagnostic(t, err, &want)
		nativeStep(s)
		missing := fixture.Error("SELECT * FROM staged", "42P01")
		missing.SelectRows = true
		nativeStep(missing)
	}
	metadata := s
	metadata.Query, metadata.Bag = `SELECT * FROM "A"`, true
	rows, err := p.Native.Query(metadata.Query)
	if err != nil {
		t.Fatal(err)
	}
	fixture.LexicalRows(t, false, rows, metadata, []bool{false, true})
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "U&") || !strings.Contains(dump.String(), `"A"`) || !strings.Contains(dump.String(), `"😀"`) {
		t.Fatalf("canonical names %s", dump.String())
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatalf("exact native dump %v\n%s", err, dump.String())
		}
	}
	step(s)
	var again bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), again.Bytes()) {
		t.Fatal("redump changed")
	}
	fixture.LexicalRows(t, false, held, s, []bool{false, true})
	canceled, cancel := context.WithCancel(p.Ctx)
	cancel()
	fixture.LexicalOrdinary(t, stuntgres.Dump(canceled, p.Native, io.Discard), context.Canceled)
	fixture.LexicalOrdinary(t, stuntgres.Reset(canceled, p.Native), context.Canceled)
	fixture.LexicalOrdinary(t, stuntgres.Restore(canceled, p.Native, bytes.NewReader(dump.Bytes())), context.Canceled)
	reading, stop := context.WithCancel(p.Ctx)
	defer stop()
	fixture.LexicalOrdinary(t, stuntgres.Restore(reading, p.Native, &fixture.LexicalCancelReader{Cancel: stop}), context.Canceled)
	failure := errors.New("unicode control I/O")
	fixture.LexicalOrdinary(t, stuntgres.Restore(p.Ctx, p.Native, fixture.LexicalFailIO{failure}), failure)
	fixture.LexicalOrdinary(t, stuntgres.Dump(p.Ctx, p.Native, fixture.LexicalFailIO{failure}), failure)
	step(s)
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(`CREATE TABLE U&"\0041"(id INT NOT NULL,U&"\+01F600" TEXT); INSERT INTO U&"\0041" VALUES (1,U&'\0041'),(2,U&'\D83D\DE00'),(3,U&''),(4,NULL),(5,U&'O''Brien\\\0001')`)); err != nil {
		t.Fatal(err)
	}
	step(s)
}
