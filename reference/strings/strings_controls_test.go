package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func stringControlRows() fixture.Step {
	return fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{
		{int64(1), "a\n\b\f\r\t\vO'Brien\\tail"}, {int64(2), "é😀\uFFFF"}, {int64(3), ""}, {int64(4), nil},
		{int64(5), "a;'b"}, {int64(6), "first\nsecond; O'Brien\\tail $1 $other$x$other$"}, {int64(7), ""}, {int64(8), "é😀"},
	})
}

func stringControlSeed(t *testing.T, p *fixture.Pair) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE t(id INT,label TEXT)", 0),
		fixture.Exec(`INSERT INTO t VALUES (1,E'a\n\b\f\r\t\vO\'Brien\\tail'),(2,E'\303\251\uD83D\uDE00\uFFFF'),(3,E''),(4,NULL)`, 4),
		fixture.Exec("INSERT INTO t VALUES (5,$$a;'b$$),(6,$T$first\nsecond; O'Brien\\tail $1 $other$x$other$$T$),(7,$$$$),(8,$_9$é😀$_9$)", 4),
	} {
		fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, s)
	}
}

func TestReferenceStringLiteralDumpRoundTrip(t *testing.T) {
	p := fixture.NewScenario(t)
	stringControlSeed(t, p)
	s := stringControlRows()
	peer, err := p.Native.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	if err := peer.PingContext(p.Ctx); err != nil {
		t.Fatal(err)
	}
	held, err := peer.QueryContext(p.Ctx, s.Query)
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
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatalf("exact dump: %v\n%s", err, dump.String())
		}
	}
	p.Step(t, s)
	rows, err := peer.QueryContext(p.Ctx, s.Query)
	if err != nil {
		t.Fatal(err)
	}
	fixture.LexicalRows(t, false, rows, s, nil)
	fixture.LexicalRows(t, false, held, s, nil)
	var again bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), again.Bytes()) || !strings.Contains(dump.String(), "'a;''b'") {
		t.Fatalf("canonical/stable dump: %s", dump.String())
	}
}

func TestReferenceStringLiteralControlsAtomicity(t *testing.T) {
	p := fixture.NewScenario(t)
	stringControlSeed(t, p)
	s := stringControlRows()
	peer, err := p.Native.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	if err := peer.PingContext(p.Ctx); err != nil {
		t.Fatal(err)
	}
	held, err := peer.QueryContext(p.Ctx, s.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	for _, suffix := range []string{"$Tag$a$tag$);", "$$a"} {
		err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE replacement(label TEXT); INSERT INTO replacement VALUES ("+suffix))
		want := fixture.ControlsReferenceDiagnostic("42601", "unterminated dollar-quoted string at or near \""+suffix+"\"")
		want.Position = "71"
		fixture.AssertDiagnostic(t, err, &want)
		fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, s)
		rows, err := peer.QueryContext(p.Ctx, s.Query)
		if err != nil {
			t.Fatal(err)
		}
		fixture.LexicalRows(t, false, rows, s, nil)
		_, err = p.Native.QueryContext(p.Ctx, "SELECT * FROM replacement")
		fixture.State(t, err, "42P01")
	}
	fixture.LexicalRows(t, false, held, s, nil)
	canceled, cancel := context.WithCancel(p.Ctx)
	cancel()
	fixture.LexicalOrdinary(t, stuntgres.Restore(canceled, p.Native, strings.NewReader("CREATE TABLE canceled(x TEXT); INSERT INTO canceled VALUES ($$v$$)")), context.Canceled)
	for _, source := range []string{`E'\0')`, `E'\xff')`, `E'\u061')`, `E'abc\`} {
		script := "CREATE TABLE replacement(label TEXT); INSERT INTO replacement VALUES ('valid'); INSERT INTO replacement VALUES (" + source
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err == nil {
			t.Fatal("bad Restore succeeded")
		}
		fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, s)
		_, err := p.Native.QueryContext(p.Ctx, "SELECT * FROM replacement")
		fixture.State(t, err, "42P01")
	}
	failure := errors.New("string control I/O")
	fixture.LexicalOrdinary(t, stuntgres.Restore(p.Ctx, p.Native, fixture.LexicalFailIO{failure}), failure)
	fixture.LexicalOrdinary(t, stuntgres.Dump(p.Ctx, p.Native, fixture.LexicalFailIO{failure}), failure)
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, s)
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(`CREATE TABLE restored(label TEXT); INSERT INTO restored VALUES (E'a\nb'),(E'\xC3\xA9');`)); err != nil {
		t.Fatal(err)
	}
	s = fixture.Select("SELECT * FROM restored", []string{"label"}, []string{"TEXT"}, [][]any{{"a\nb"}, {"é"}})
	s.Bag = true
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, s)
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE restored(id INT,label TEXT); INSERT INTO restored VALUES (1,$$a;'b$$),(2,$T$first\nsecond; $other$x$other$$T$);")); err != nil {
		t.Fatal(err)
	}
	s = fixture.Select("SELECT * FROM restored ORDER BY id", []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a;'b"}, {int64(2), "first\nsecond; $other$x$other$"}})
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, s)
	rows, err := peer.QueryContext(p.Ctx, s.Query)
	if err != nil {
		t.Fatal(err)
	}
	fixture.LexicalRows(t, false, rows, s, nil)
}
