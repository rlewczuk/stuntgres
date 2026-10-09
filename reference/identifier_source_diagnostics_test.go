package reference_test

import (
	"bytes"
	"fmt"
	"stuntgres"
	"testing"
)

func TestReferenceIdentifierDiagnosticSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		cases := append([]struct{ q, word, pos string }{}, identifierSyntaxCases...)
		cases = append(cases, []struct{ q, word, pos string }{
			{"SELECT select FROM abort", "select", "8"},
			{"SELECT by char FROM insert", "char", "11"},
			{"SELECT by day FROM insert", "day", "11"},
		}...)
		for _, tc := range cases {
			want := controlsReferenceDiagnostic("42601", fmt.Sprintf("syntax error at or near %q", tc.word))
			want.Position = tc.pos
			r, err := backend.db.Exec(tc.q)
			if r != nil {
				t.Fatal("failed Exec published result")
			}
			referenceAssertDiagnostic(t, err, &want)
			rows, err := backend.db.Query(tc.q)
			if rows != nil {
				rows.Close()
				t.Fatal("failed Query published rows")
			}
			referenceAssertDiagnostic(t, err, &want)
			held, err := backend.db.Prepare(tc.q)
			if held != nil {
				held.Close()
				t.Fatal("failed Prepare published statement")
			}
			referenceAssertDiagnostic(t, err, &want)
		}
	}
}

func TestReferenceIdentifierContextSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		for _, s := range []referenceStep{referenceExec("CREATE TABLE t (x INT)", 0), referenceExec("INSERT INTO t VALUES (7)", 1)} {
			r, err := backend.db.Exec(s.query)
			deleteReferenceCount(t, r, err, s.affected)
		}
		if backend.pg {
			for _, name := range []string{`f`, `abort`, `authorization`, `"between"`} {
				r, err := backend.db.Exec("CREATE FUNCTION public." + name + "(integer) RETURNS integer LANGUAGE sql IMMUTABLE AS 'SELECT $1'")
				deleteReferenceCount(t, r, err, 0)
			}
		}
		for _, tc := range []struct {
			q    string
			want bool
		}{{"SELECT x isnull FROM t", false}, {"SELECT x notnull FROM t", true}} {
			r, err := backend.db.Exec(tc.q)
			deleteReferenceCount(t, r, err, 1)
			s := referenceSelect(tc.q, []string{"?column?"}, []string{"BOOL"}, [][]any{{tc.want}})
			rows, err := backend.db.Query(tc.q)
			deleteReferenceConsume(t, backend.pg, rows, err, s)
			held, err := backend.db.Prepare(tc.q)
			if err != nil {
				t.Fatal(err)
			}
			rows, err = held.Query()
			deleteReferenceConsume(t, backend.pg, rows, err, s)
			if err := held.Close(); err != nil {
				t.Fatal(err)
			}
		}
		for _, tc := range identifierContextCases {
			want := controlsReferenceDiagnostic(tc.code, tc.message)
			want.Position = tc.position
			r, err := backend.db.Exec(tc.q)
			if backend.pg && tc.code == "0A000" {
				deleteReferenceCount(t, r, err, 1)
			} else {
				if r != nil {
					t.Fatal("failed Exec published result")
				}
				referenceAssertDiagnostic(t, err, &want)
			}
			rows, err := backend.db.Query(tc.q)
			if backend.pg && tc.code == "0A000" {
				deleteReferenceConsume(t, true, rows, err, referenceSelect(tc.q, []string{tc.column}, []string{tc.typ}, [][]any{{tc.row}}))
			} else {
				if rows != nil {
					rows.Close()
					t.Fatal("failed Query published rows")
				}
				referenceAssertDiagnostic(t, err, &want)
			}
			held, err := backend.db.Prepare(tc.q)
			if backend.pg && tc.code == "0A000" {
				if err != nil {
					t.Fatal(err)
				}
				if err := held.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				if held != nil {
					held.Close()
					t.Fatal("failed Prepare published statement")
				}
				referenceAssertDiagnostic(t, err, &want)
			}
		}
	}
}

func TestReferenceIdentifierLaterCallSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		for _, s := range []referenceStep{referenceExec("CREATE TABLE t (x INT)", 0), referenceExec("INSERT INTO t VALUES (7)", 1)} {
			r, err := backend.db.Exec(s.query)
			deleteReferenceCount(t, r, err, s.affected)
		}
		if backend.pg {
			for _, name := range []string{`f`, `abort`, `authorization`, `"between"`} {
				r, err := backend.db.Exec("CREATE FUNCTION public." + name + "(integer) RETURNS integer LANGUAGE sql IMMUTABLE AS 'SELECT $1'")
				deleteReferenceCount(t, r, err, 0)
			}
		}
		var before, after bytes.Buffer
		if !backend.pg {
			if err := stuntgres.Dump(p.ctx, p.native, &before); err != nil {
				t.Fatal(err)
			}
		}
		for _, tc := range identifierLaterCallCases {
			want := controlsReferenceDiagnostic(tc.code, tc.message)
			want.Position = tc.position
			r, err := backend.db.Exec(tc.q)
			if backend.pg && tc.code == "0A000" {
				deleteReferenceCount(t, r, err, 1)
			} else {
				if r != nil {
					t.Fatal("failed Exec published result")
				}
				referenceAssertDiagnostic(t, err, &want)
			}
			rows, err := backend.db.Query(tc.q)
			if backend.pg && tc.code == "0A000" {
				deleteReferenceConsume(t, true, rows, err, referenceSelect(tc.q, tc.columns, tc.types, tc.rows))
			} else {
				if rows != nil {
					rows.Close()
					t.Fatal("failed Query published rows")
				}
				referenceAssertDiagnostic(t, err, &want)
			}
			held, err := backend.db.Prepare(tc.q)
			if backend.pg && tc.code == "0A000" {
				if err != nil {
					t.Fatal(err)
				}
				if err := held.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				if held != nil {
					held.Close()
					t.Fatal("failed Prepare published statement")
				}
				referenceAssertDiagnostic(t, err, &want)
			}
		}
		r, err := backend.db.Exec("CREATE TABLE staged (x INT); SELECT x, f(x) FROM t")
		if backend.pg {
			deleteReferenceCount(t, r, err, 1)
		} else {
			if r != nil {
				t.Fatal("failed prefix published result")
			}
			want := controlsReferenceDiagnostic("0A000", "functions are unsupported")
			want.Position = "40"
			referenceAssertDiagnostic(t, err, &want)
			if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before.Bytes(), after.Bytes()) {
				t.Fatal("later rejection changed catalog")
			}
		}
		rows, err := backend.db.Query("SELECT x FROM t ORDER BY x")
		deleteReferenceConsume(t, backend.pg, rows, err, referenceSelect("SELECT x FROM t ORDER BY x", []string{"x"}, []string{"INT4"}, [][]any{{int64(7)}}))
	}
}

func TestReferenceIdentifierControlSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		for _, s := range []referenceStep{referenceExec("CREATE TABLE sentinel (x INT)", 0), referenceExec("INSERT INTO sentinel VALUES (7)", 1)} {
			r, err := backend.db.Exec(s.query)
			deleteReferenceCount(t, r, err, s.affected)
		}
		for _, q := range []string{"CREATE TABLE staged (x INT); CREATE TABLE select (x INT)", "CREATE TABLE staged (x INT); SELECT select FROM sentinel"} {
			r, err := backend.db.Exec(q)
			if err == nil || r != nil {
				t.Fatal("malformed script succeeded", r, err)
			}
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
