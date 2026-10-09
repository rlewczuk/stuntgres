package reference_test

import (
	"bytes"
	"fmt"
	"stuntgres"
	"testing"
)

func TestReferenceIdentifierControls(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE sentinel (x INT)", 0))
	p.step(t, referenceExec("INSERT INTO sentinel VALUES (7)", 1))
	for _, q := range []string{"CREATE TABLE staged (x INT); CREATE TABLE select (x INT)", "CREATE TABLE staged (x INT); SELECT select FROM sentinel"} {
		for _, backend := range p.backends() {
			result, err := backend.db.ExecContext(p.ctx, q)
			if err == nil || result != nil {
				t.Fatal("malformed whole script", result, err)
			}
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT x FROM sentinel", []string{"x"}, []string{"INT4"}, [][]any{{int64(7)}}))
		}
	}
	var before, after bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &before); err != nil {
		t.Fatal(err)
	}
	err := stuntgres.Restore(p.ctx, p.native, bytes.NewBufferString("CREATE TABLE staged (x INT); CREATE TABLE select (x INT)"))
	want := controlsReferenceDiagnostic("42601", `syntax error at or near "select"`)
	want.Position = "43"
	referenceAssertDiagnostic(t, err, &want)
	if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before.Bytes(), after.Bytes()) {
		t.Fatal("failed restore changed catalog")
	}
}

func TestReferenceIdentifierKeywordContractDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE abort (by INT)", 0))
	p.step(t, referenceExec("CREATE TABLE insert (by INT)", 0))
	for _, tc := range []struct{ q, word, pos string }{
		{"CREATE TABLE select (x INT)", "select", "14"}, {"CREATE TABLE SELECT (x INT)", "SELECT", "14"}, {"/*é*/ CREATE TABLE SELECT (x INT)", "SELECT", "20"}, {"CREATE TABLE t (authorization INT)", "authorization", "17"}, {"SELECT select FROM abort", "select", "8"},
		{"SELECT by char FROM insert", "char", "11"}, {"SELECT by day FROM insert", "day", "11"},
	} {
		for _, route := range []string{"exec", "query", "prepare"} {
			want := controlsReferenceDiagnostic("42601", fmt.Sprintf("syntax error at or near %q", tc.word))
			want.Position = tc.pos
			s := referenceErrorDiagnostic(tc.q, want)
			s.selectRows = route == "query"
			s.prepared = route == "prepare"
			s.errorStage = "execute"
			if s.prepared {
				s.errorStage = "prepare"
			}
			p.step(t, s)
		}
	}
}

func TestReferenceIdentifierKeywordRetainedReader(t *testing.T) {
	p := newReferenceScenario(t)
	for _, s := range []referenceStep{
		referenceExec("CREATE TABLE abort (by INT,between TEXT,values BOOL,integer BIGINT,char TEXT)", 0),
		referenceExec("INSERT INTO abort VALUES(1,'one',TRUE,10,'a'),(2,'two',FALSE,20,'b'),(3,'three',TRUE,30,'c')", 3),
	} {
		p.step(t, s)
	}
	const q = "SELECT by,between,values,integer,char FROM public.abort WHERE by=2 ORDER BY by DESC"
	old, err := p.native.QueryContext(p.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	var dump, after bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	err = stuntgres.Restore(p.ctx, p.native, bytes.NewBufferString("CREATE TABLE staged (x INT); CREATE TABLE select (x INT)"))
	want := controlsReferenceDiagnostic("42601", `syntax error at or near "select"`)
	want.Position = "43"
	referenceAssertDiagnostic(t, err, &want)
	if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), after.Bytes()) {
		t.Fatal("failed restore changed catalog")
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	columns, err := old.Columns()
	if err != nil || fmt.Sprint(columns) != "[by between values integer char]" {
		t.Fatal(columns, err)
	}
	metadata, err := old.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for i, typ := range []string{"INT4", "TEXT", "BOOL", "INT8", "TEXT"} {
		if metadata[i].DatabaseTypeName() != typ {
			t.Fatal(metadata[i])
		}
	}
	if !old.Next() {
		t.Fatal(old.Err())
	}
	var by, integer int64
	var between, char string
	var value bool
	if err := old.Scan(&by, &between, &value, &integer, &char); err != nil || by != 2 || between != "two" || value || integer != 20 || char != "b" {
		t.Fatal(by, between, value, integer, char, err)
	}
	if old.Next() || old.Err() != nil {
		t.Fatal(old.Err())
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	p.step(t, referenceSelect(q, []string{"by", "between", "values", "integer", "char"}, []string{"INT4", "TEXT", "BOOL", "INT8", "TEXT"}, [][]any{{int64(2), "two", false, int64(20), "b"}}))
}
