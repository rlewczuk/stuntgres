package reference_test

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestReferenceFloatInputDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE f(r REAL,d DOUBLE PRECISION)", 0))
	for _, tc := range []struct{ q, code, message, position string }{
		{"INSERT INTO f VALUES('oops','1')", "22P02", `invalid input syntax for type real: "oops"`, "22"},
		{"INSERT INTO f VALUES('1','oops')", "22P02", `invalid input syntax for type double precision: "oops"`, "26"},
		{"INSERT INTO f VALUES('1e999','1')", "22003", `"1e999" is out of range for type real`, "22"},
		{"INSERT INTO f VALUES('1','1e-999')", "22003", `"1e-999" is out of range for type double precision`, "26"},
		{"SELECT r FROM f WHERE r='oops'", "22P02", `invalid input syntax for type real: "oops"`, "25"},
		{"/*é*/ SELECT r FROM f WHERE r='oops'", "22P02", `invalid input syntax for type real: "oops"`, "31"},
		{"SELECT r FROM f WHERE r=TRUE", "42883", "operator does not exist: real = boolean", "24"},
	} {
		for _, prepared := range []bool{false, true} {
			want := referenceDiagnostic{Code: tc.code, Message: tc.message, Position: tc.position}
			if tc.code == "42883" {
				want.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
			}
			s := referenceImplicitError(tc.q, want)
			s.prepared = prepared
			s.selectRows = strings.Contains(tc.q, "SELECT")
			s.errorStage = "execute"
			if prepared {
				s.errorStage = "prepare"
			}
			p.step(t, s)
			if s.selectRows && !prepared {
				s.selectRows = false
				p.step(t, s)
			}
		}
	}
	for _, backend := range p.backends() {
		func() {
			q := "INSERT INTO f VALUES($1,'1')"
			stmt, err := backend.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: backend.db, statement: stmt, query: q}
			for _, arg := range []string{"oops", "1e999", "1e-46"} {
				code, message := "22P02", `invalid input syntax for type real: "oops"`
				if arg != "oops" {
					code, message = "22003", `"`+arg+`" is out of range for type real`
				}
				s := referenceImplicitError(q, referenceDiagnostic{Code: code, Message: message, Context: "unnamed portal parameter $1 = '...'"}, arg)
				s.errorStage = "execute"
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
				referenceRunStep(t, p.ctx, held, backend.recorder, s)
			}
		}()
	}
}

func TestReferenceFloatAtomicityAndCancellation(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE f(r REAL,d DOUBLE PRECISION)", 0))
	p.step(t, referenceExec("INSERT INTO f VALUES('99','99')", 1))
	unchanged := referenceSelect("SELECT * FROM f", []string{"r", "d"}, []string{"FLOAT4", "FLOAT8"}, [][]any{{float64(99), float64(99)}})
	for _, tc := range []struct{ q, code, message, position string }{
		{"INSERT INTO f VALUES('1','1'),('oops','2')", "22P02", `invalid input syntax for type real: "oops"`, "32"},
		{"INSERT INTO f VALUES('1','1'),('2','1e999')", "22003", `"1e999" is out of range for type double precision`, "36"},
	} {
		for _, prepared := range []bool{false, true} {
			s := referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
			s.prepared, s.errorStage = prepared, "execute"
			if prepared {
				s.errorStage = "prepare"
			}
			p.step(t, s)
			p.step(t, unchanged)
		}
	}
	p.step(t, referenceExec("CREATE TABLE nn(r REAL NOT NULL)", 0))
	failure := referenceImplicitError("INSERT INTO nn VALUES('1'),(NULL)", referenceDiagnostic{Code: "23502", Message: `null value in column "r" of relation "nn" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "nn", Column: "r"})
	failure.errorStage = "execute"
	p.step(t, failure)
	p.step(t, referenceSelect("SELECT * FROM nn", []string{"r"}, []string{"FLOAT4"}, nil))
	for _, backend := range p.backends() {
		ctx, cancel := context.WithCancel(p.ctx)
		cancel()
		if _, err := backend.db.ExecContext(ctx, "INSERT INTO f VALUES('1','2')"); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		rows, err := backend.db.QueryContext(ctx, "SELECT * FROM f")
		if rows != nil {
			rows.Close()
			t.Fatal("canceled Query exposed cursor")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		referenceRunStep(t, p.ctx, backend.db, backend.recorder, unchanged)
	}
}
