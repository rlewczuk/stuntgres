package reference_test

import (
	"bytes"
	"fmt"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceIdentifierControls(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE sentinel (x INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO sentinel VALUES (7)", 1))
	for _, q := range []string{"CREATE TABLE staged (x INT); CREATE TABLE select (x INT)", "CREATE TABLE staged (x INT); SELECT select FROM sentinel"} {
		for _, backend := range p.Backends() {
			result, err := backend.DB.ExecContext(p.Ctx, q)
			if err == nil || result != nil {
				t.Fatal("malformed whole script", result, err)
			}
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT x FROM sentinel", []string{"x"}, []string{"INT4"}, [][]any{{int64(7)}}))
		}
	}
	var before, after bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
		t.Fatal(err)
	}
	err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewBufferString("CREATE TABLE staged (x INT); CREATE TABLE select (x INT)"))
	want := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "select"`)
	want.Position = "43"
	fixture.AssertDiagnostic(t, err, &want)
	if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before.Bytes(), after.Bytes()) {
		t.Fatal("failed restore changed catalog")
	}
}

func TestReferenceIdentifierKeywordContractDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE abort (by INT)", 0))
	p.Step(t, fixture.Exec("CREATE TABLE insert (by INT)", 0))
	for _, tc := range []struct{ q, word, pos string }{
		{"CREATE TABLE select (x INT)", "select", "14"}, {"CREATE TABLE SELECT (x INT)", "SELECT", "14"}, {"/*é*/ CREATE TABLE SELECT (x INT)", "SELECT", "20"}, {"CREATE TABLE t (authorization INT)", "authorization", "17"}, {"SELECT select FROM abort", "select", "8"},
		{"SELECT by char FROM insert", "char", "11"}, {"SELECT by day FROM insert", "day", "11"},
	} {
		for _, route := range []string{"exec", "query", "prepare"} {
			want := fixture.ControlsReferenceDiagnostic("42601", fmt.Sprintf("syntax error at or near %q", tc.word))
			want.Position = tc.pos
			s := fixture.ErrorDiagnostic(tc.q, want)
			s.SelectRows = route == "query"
			s.Prepared = route == "prepare"
			s.ErrorStage = "execute"
			if s.Prepared {
				s.ErrorStage = "prepare"
			}
			p.Step(t, s)
		}
	}
}

func TestReferenceIdentifierKeywordRetainedReader(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE abort (by INT,between TEXT,values BOOL,integer BIGINT,char TEXT)", 0),
		fixture.Exec("INSERT INTO abort VALUES(1,'one',TRUE,10,'a'),(2,'two',FALSE,20,'b'),(3,'three',TRUE,30,'c')", 3),
	} {
		p.Step(t, s)
	}
	const q = "SELECT by,between,values,integer,char FROM public.abort WHERE by=2 ORDER BY by DESC"
	old, err := p.Native.QueryContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	var dump, after bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewBufferString("CREATE TABLE staged (x INT); CREATE TABLE select (x INT)"))
	want := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "select"`)
	want.Position = "43"
	fixture.AssertDiagnostic(t, err, &want)
	if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), after.Bytes()) {
		t.Fatal("failed restore changed catalog")
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
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
	p.Step(t, fixture.Select(q, []string{"by", "between", "values", "integer", "char"}, []string{"INT4", "TEXT", "BOOL", "INT8", "TEXT"}, [][]any{{int64(2), "two", false, int64(20), "b"}}))
}
