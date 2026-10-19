package reference_test

import (
	"bytes"
	"fmt"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceIdentifierDiagnosticSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		cases := append([]struct{ q, word, pos string }{}, identifierSyntaxCases...)
		cases = append(cases, []struct{ q, word, pos string }{
			{"SELECT select FROM abort", "select", "8"},
			{"SELECT by char FROM insert", "char", "11"},
			{"SELECT by day FROM insert", "day", "11"},
		}...)
		for _, tc := range cases {
			want := fixture.ControlsReferenceDiagnostic("42601", fmt.Sprintf("syntax error at or near %q", tc.word))
			want.Position = tc.pos
			r, err := backend.DB.Exec(tc.q)
			if r != nil {
				t.Fatal("failed Exec published result")
			}
			fixture.AssertDiagnostic(t, err, &want)
			rows, err := backend.DB.Query(tc.q)
			if rows != nil {
				rows.Close()
				t.Fatal("failed Query published rows")
			}
			fixture.AssertDiagnostic(t, err, &want)
			held, err := backend.DB.Prepare(tc.q)
			if held != nil {
				held.Close()
				t.Fatal("failed Prepare published statement")
			}
			fixture.AssertDiagnostic(t, err, &want)
		}
	}
}

func TestReferenceIdentifierContextSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE t (x INT)", 0), fixture.Exec("INSERT INTO t VALUES (7)", 1)} {
			r, err := backend.DB.Exec(s.Query)
			fixture.DeleteReferenceCount(t, r, err, s.Affected)
		}
		for _, tc := range []struct {
			q    string
			want bool
		}{{"SELECT x isnull FROM t", false}, {"SELECT x notnull FROM t", true}} {
			r, err := backend.DB.Exec(tc.q)
			fixture.DeleteReferenceCount(t, r, err, 1)
			s := fixture.Select(tc.q, []string{"?column?"}, []string{"BOOL"}, [][]any{{tc.want}})
			rows, err := backend.DB.Query(tc.q)
			fixture.DeleteReferenceConsume(t, backend.PG, rows, err, s)
			held, err := backend.DB.Prepare(tc.q)
			if err != nil {
				t.Fatal(err)
			}
			rows, err = held.Query()
			fixture.DeleteReferenceConsume(t, backend.PG, rows, err, s)
			if err := held.Close(); err != nil {
				t.Fatal(err)
			}
		}
		for _, present := range []bool{false, true} {
			if present {
				for _, name := range []string{`f`, `abort`, `authorization`, `"between"`} {
					r, err := backend.DB.Exec("CREATE FUNCTION public." + name + "(integer) RETURNS integer LANGUAGE sql AS 'SELECT $1'")
					fixture.DeleteReferenceCount(t, r, err, 0)
				}
			}
			for _, tc := range identifierContextCases {
				want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				want.Position = tc.position
				if tc.code == "42883" {
					want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				}
				r, err := backend.DB.Exec(tc.q)
				if present && tc.code == "42883" {
					fixture.DeleteReferenceCount(t, r, err, 1)
				} else {
					if r != nil {
						t.Fatal("failed Exec published result")
					}
					fixture.AssertDiagnostic(t, err, &want)
				}
				rows, err := backend.DB.Query(tc.q)
				if present && tc.code == "42883" {
					fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Select(tc.q, []string{tc.column}, []string{tc.typ}, [][]any{{tc.row}}))
				} else {
					if rows != nil {
						rows.Close()
						t.Fatal("failed Query published rows")
					}
					fixture.AssertDiagnostic(t, err, &want)
				}
				held, err := backend.DB.Prepare(tc.q)
				if present && tc.code == "42883" {
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
					fixture.AssertDiagnostic(t, err, &want)
				}
			}
		}
	}
}

func TestReferenceIdentifierLaterCallSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE t (x INT)", 0), fixture.Exec("INSERT INTO t VALUES (7)", 1)} {
			r, err := backend.DB.Exec(s.Query)
			fixture.DeleteReferenceCount(t, r, err, s.Affected)
		}
		var before, after bytes.Buffer
		if !backend.PG {
			if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
				t.Fatal(err)
			}
		}
		for _, present := range []bool{false, true} {
			if present {
				for _, name := range []string{`f`, `abort`, `authorization`, `"between"`} {
					r, err := backend.DB.Exec("CREATE FUNCTION public." + name + "(integer) RETURNS integer LANGUAGE sql AS 'SELECT $1'")
					fixture.DeleteReferenceCount(t, r, err, 0)
				}
			}
			for _, tc := range identifierLaterCallCases {
				want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				want.Position = tc.position
				if tc.code == "42883" {
					want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				}
				r, err := backend.DB.Exec(tc.q)
				if present && tc.code == "42883" {
					fixture.DeleteReferenceCount(t, r, err, 1)
				} else {
					if r != nil {
						t.Fatal("failed Exec published result")
					}
					fixture.AssertDiagnostic(t, err, &want)
				}
				rows, err := backend.DB.Query(tc.q)
				if present && tc.code == "42883" {
					fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Select(tc.q, tc.columns, tc.types, tc.rows))
				} else {
					if rows != nil {
						rows.Close()
						t.Fatal("failed Query published rows")
					}
					fixture.AssertDiagnostic(t, err, &want)
				}
				held, err := backend.DB.Prepare(tc.q)
				if present && tc.code == "42883" {
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
					fixture.AssertDiagnostic(t, err, &want)
				}
			}
			r, err := backend.DB.Exec("CREATE TABLE staged (x INT); SELECT x, f(x) FROM t")
			if present {
				fixture.DeleteReferenceCount(t, r, err, 1)
				rows, err := backend.DB.Query("SELECT x FROM staged")
				fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Select("SELECT x FROM staged", []string{"x"}, []string{"INT4"}, nil))
			} else {
				if r != nil {
					t.Fatal("failed prefix published result")
				}
				want := fixture.ControlsReferenceDiagnostic("42883", "function f(integer) does not exist")
				want.Position = "40"
				want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				fixture.AssertDiagnostic(t, err, &want)
				if !backend.PG {
					if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(before.Bytes(), after.Bytes()) {
						t.Fatal("later rejection changed catalog")
					}
				}
			}
		}
		rows, err := backend.DB.Query("SELECT x FROM t ORDER BY x")
		fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Select("SELECT x FROM t ORDER BY x", []string{"x"}, []string{"INT4"}, [][]any{{int64(7)}}))
	}
}

func TestReferenceIdentifierControlSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE sentinel (x INT)", 0), fixture.Exec("INSERT INTO sentinel VALUES (7)", 1)} {
			r, err := backend.DB.Exec(s.Query)
			fixture.DeleteReferenceCount(t, r, err, s.Affected)
		}
		for _, q := range []string{"CREATE TABLE staged (x INT); CREATE TABLE select (x INT)", "CREATE TABLE staged (x INT); SELECT select FROM sentinel"} {
			r, err := backend.DB.Exec(q)
			if err == nil || r != nil {
				t.Fatal("malformed script succeeded", r, err)
			}
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
