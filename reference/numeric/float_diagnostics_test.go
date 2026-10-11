package reference_test

import (
	"context"
	"errors"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceFloatInputDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE f(r REAL,d DOUBLE PRECISION)", 0))
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
			want := fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position}
			if tc.code == "42883" {
				want.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
			}
			s := fixture.ImplicitError(tc.q, want)
			s.Prepared = prepared
			s.SelectRows = strings.Contains(tc.q, "SELECT")
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			p.Step(t, s)
			if s.SelectRows && !prepared {
				s.SelectRows = false
				p.Step(t, s)
			}
		}
	}
	for _, backend := range p.Backends() {
		func() {
			q := "INSERT INTO f VALUES($1,'1')"
			stmt, err := backend.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: q}
			for _, arg := range []string{"oops", "1e999", "1e-46"} {
				code, message := "22P02", `invalid input syntax for type real: "oops"`
				if arg != "oops" {
					code, message = "22003", `"`+arg+`" is out of range for type real`
				}
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message, Context: "unnamed portal parameter $1 = '...'"}, arg)
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
				fixture.RunStep(t, p.Ctx, held, backend.Recorder, s)
			}
		}()
	}
}

func TestReferenceFloatAtomicityAndCancellation(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE f(r REAL,d DOUBLE PRECISION)", 0))
	p.Step(t, fixture.Exec("INSERT INTO f VALUES('99','99')", 1))
	unchanged := fixture.Select("SELECT * FROM f", []string{"r", "d"}, []string{"FLOAT4", "FLOAT8"}, [][]any{{float64(99), float64(99)}})
	for _, tc := range []struct{ q, code, message, position string }{
		{"INSERT INTO f VALUES('1','1'),('oops','2')", "22P02", `invalid input syntax for type real: "oops"`, "32"},
		{"INSERT INTO f VALUES('1','1'),('2','1e999')", "22003", `"1e999" is out of range for type double precision`, "36"},
	} {
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
			s.Prepared, s.ErrorStage = prepared, "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			p.Step(t, s)
			p.Step(t, unchanged)
		}
	}
	p.Step(t, fixture.Exec("CREATE TABLE nn(r REAL NOT NULL)", 0))
	failure := fixture.ImplicitError("INSERT INTO nn VALUES('1'),(NULL)", fixture.Diagnostic{Code: "23502", Message: `null value in column "r" of relation "nn" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "nn", Column: "r"})
	failure.ErrorStage = "execute"
	p.Step(t, failure)
	p.Step(t, fixture.Select("SELECT * FROM nn", []string{"r"}, []string{"FLOAT4"}, nil))
	for _, backend := range p.Backends() {
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if _, err := backend.DB.ExecContext(ctx, "INSERT INTO f VALUES('1','2')"); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		rows, err := backend.DB.QueryContext(ctx, "SELECT * FROM f")
		if rows != nil {
			rows.Close()
			t.Fatal("canceled Query exposed cursor")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, unchanged)
	}
}
