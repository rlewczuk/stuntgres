package reference_test

import (
	"bytes"
	"context"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceStringLiteralSourceDumpRoundTrip(t *testing.T) {
	p := newReferenceScenario(t)
	step := func(s referenceStep) { s.nonContext = true; p.step(t, s) }
	for _, s := range []referenceStep{
		referenceExec("CREATE TABLE t(id INT,label TEXT)", 0),
		referenceExec(`INSERT INTO t VALUES (1,E'a\n\b\f\r\t\vO\'Brien\\tail'),(2,E'\303\251\uD83D\uDE00\uFFFF'),(3,E''),(4,NULL)`, 4),
		referenceExec("INSERT INTO t VALUES (5,$$a;'b$$),(6,$T$first\nsecond; O'Brien\\tail $1 $other$x$other$$T$),(7,$$$$),(8,$_9$é😀$_9$)", 4),
	} {
		s.nonContext = true
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, s)
	}
	s := stringControlRows()
	s.nonContext = true
	peer := p.memory
	if err := peer.Ping(); err != nil {
		t.Fatal(err)
	}
	held, err := peer.Query(s.query)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
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
		if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
			t.Fatalf("exact dump: %v\n%s", err, dump.String())
		}
	}
	step(s)
	rows, err := peer.Query(s.query)
	if err != nil {
		t.Fatal(err)
	}
	lexicalRows(t, false, rows, s, nil)
	lexicalRows(t, false, held, s, nil)
	var again bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &again); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), again.Bytes()) || !strings.Contains(dump.String(), "'a;''b'") {
		t.Fatalf("canonical/stable dump: %s", dump.String())
	}
}

func TestReferenceStringLiteralSourceControlsAtomicity(t *testing.T) {
	p := newReferenceScenario(t)
	for _, s := range []referenceStep{
		referenceExec("CREATE TABLE t(id INT,label TEXT)", 0),
		referenceExec(`INSERT INTO t VALUES (1,E'a\n\b\f\r\t\vO\'Brien\\tail'),(2,E'\303\251\uD83D\uDE00\uFFFF'),(3,E''),(4,NULL)`, 4),
		referenceExec("INSERT INTO t VALUES (5,$$a;'b$$),(6,$T$first\nsecond; O'Brien\\tail $1 $other$x$other$$T$),(7,$$$$),(8,$_9$é😀$_9$)", 4),
	} {
		s.nonContext = true
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, s)
	}
	s := stringControlRows()
	s.nonContext = true
	peer := p.memory
	if err := peer.Ping(); err != nil {
		t.Fatal(err)
	}
	held, err := peer.Query(s.query)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	for _, suffix := range []string{"$Tag$a$tag$);", "$$a"} {
		err := stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE replacement(label TEXT); INSERT INTO replacement VALUES ("+suffix))
		want := controlsReferenceDiagnostic("42601", "unterminated dollar-quoted string at or near \""+suffix+"\"")
		want.Position = "71"
		referenceAssertDiagnostic(t, err, &want)
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, s)
		rows, err := peer.Query(s.query)
		if err != nil {
			t.Fatal(err)
		}
		lexicalRows(t, false, rows, s, nil)
		_, err = p.native.Query("SELECT * FROM replacement")
		referenceState(t, err, "42P01")
	}
	lexicalRows(t, false, held, s, nil)
	canceled, cancel := context.WithCancel(p.ctx)
	cancel()
	lexicalOrdinary(t, stuntgres.Restore(canceled, p.native, strings.NewReader("CREATE TABLE canceled(x TEXT); INSERT INTO canceled VALUES ($$v$$)")), context.Canceled)
	canceledRows, canceledErr := peer.Query(s.query)
	if canceledErr != nil {
		t.Fatal(canceledErr)
	}
	lexicalRows(t, false, canceledRows, s, nil)
	for _, source := range []string{`E'\0')`, `E'\xff')`, `E'\u061')`, `E'abc\`} {
		script := "CREATE TABLE replacement(label TEXT); INSERT INTO replacement VALUES ('valid'); INSERT INTO replacement VALUES (" + source
		if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(script)); err == nil {
			t.Fatal("bad Restore succeeded")
		}
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, s)
		_, err := p.native.Query("SELECT * FROM replacement")
		referenceState(t, err, "42P01")
	}
	if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(`CREATE TABLE restored(label TEXT); INSERT INTO restored VALUES (E'a\nb'),(E'\xC3\xA9');`)); err != nil {
		t.Fatal(err)
	}
	s = referenceSelect("SELECT * FROM restored", []string{"label"}, []string{"TEXT"}, [][]any{{"a\nb"}, {"é"}})
	s.bag = true
	s.nonContext = true
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, s)
	if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE restored(id INT,label TEXT); INSERT INTO restored VALUES (1,$$a;'b$$),(2,$T$first\nsecond; $other$x$other$$T$);")); err != nil {
		t.Fatal(err)
	}
	s = referenceSelect("SELECT * FROM restored ORDER BY id", []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a;'b"}, {int64(2), "first\nsecond; $other$x$other$"}})
	s.nonContext = true
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, s)
	rows, err := peer.Query(s.query)
	if err != nil {
		t.Fatal(err)
	}
	lexicalRows(t, false, rows, s, nil)
}
