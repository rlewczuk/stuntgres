package reference_test

import (
	"context"
	"testing"
	"time"
)

func TestReferenceMathScalarParameterDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	for _, tc := range []struct {
		query, input, message string
		args                  []any
	}{
		{"SELECT factorial($1)", "1.5", `invalid input syntax for type bigint: "1.5"`, nil},
		{"SELECT exp($1)", "oops", `invalid input syntax for type double precision: "oops"`, nil},
		{"SELECT div($1,$2)", "oops", `invalid input syntax for type numeric: "oops"`, []any{"2"}},
	} {
		args := append([]any{tc.input}, tc.args...)
		s := referenceImplicitError(tc.query, referenceDiagnostic{Code: "22P02", Message: tc.message}, args...)
		s.selectRows = true
		s.errorStage = "execute"
		pg := *s.diagnostic
		pg.Context = "unnamed portal parameter $1 = '...'"
		s.diagnostic = &pg
		p.step(t, s)
		p.step(t, referencePrepared(s))
	}
}

func TestReferenceMathScalarLookup(t *testing.T) {
	p := newReferenceScenario(t)
	if p.postgres != nil {
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if _, err := p.postgres.ExecContext(ctx, "DROP SCHEMA IF EXISTS math_scalar_lookup CASCADE; DROP SCHEMA IF EXISTS other CASCADE"); err != nil {
				t.Errorf("math namespace cleanup: %v", err)
			}
		})
	}
	p.step(t, referenceExec("CREATE TABLE m(n NUMERIC,r REAL,d DOUBLE PRECISION,s SMALLINT,i INTEGER)", 0))
	p.step(t, referenceExec("CREATE SCHEMA math_scalar_lookup", 0))
	p.step(t, referenceExec("CREATE SCHEMA other", 0))
	hint := "No function matches the given name and argument types. You might need to add explicit type casts."
	for _, tc := range []struct{ query, message, position string }{
		{"SELECT factorial(n) FROM m", "function factorial(numeric) does not exist", "8"},
		{"/*é*/ SELECT factorial(n) FROM m", "function factorial(numeric) does not exist", "14"},
		{"SELECT factorial(d) FROM m", "function factorial(double precision) does not exist", "8"},
		{"SELECT div(d,d) FROM m", "function div(double precision, double precision) does not exist", "8"},
		{"SELECT div(r,r) FROM m", "function div(real, real) does not exist", "8"},
		{"SELECT math_scalar_lookup.abs(1)", "function math_scalar_lookup.abs(integer) does not exist", "8"},
		{"SELECT other.abs(1)", "function other.abs(integer) does not exist", "8"},
	} {
		s := referenceImplicitError(tc.query, referenceDiagnostic{Code: "42883", Message: tc.message, Position: tc.position, Hint: hint})
		s.selectRows = true
		s.errorStage = "execute"
		p.step(t, s)
		s.errorStage = "prepare"
		p.step(t, referencePrepared(s))
	}
	for _, tc := range []struct {
		query string
		names []string
	}{
		{"SELECT abs(n) AS magnitude,ceil(n) AS upper,ceiling(n) AS alias,floor(n) AS lower,exp(n) AS growth,div(n,1) AS quotient,factorial(i) AS product FROM m", []string{"magnitude", "upper", "alias", "lower", "growth", "quotient", "product"}},
		{"SELECT pg_catalog.abs(n),pg_catalog.ceil(n),pg_catalog.ceiling(n),pg_catalog.floor(n),pg_catalog.exp(n),pg_catalog.div(n,1),pg_catalog.factorial(i) FROM m", []string{"abs", "ceil", "ceiling", "floor", "exp", "div", "factorial"}},
	} {
		s := referenceSelect(tc.query, tc.names, []string{"NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC"}, nil)
		p.step(t, s)
		p.step(t, referencePrepared(s))
	}
	p.step(t, referenceExec("INSERT INTO m VALUES(-5.0,-1.5,-1.5,5,5)", 1))
	for _, query := range []string{`SELECT s,"abs"(s),ceil(r),ceiling(n),factorial(s),factorial(abs(s)) FROM m`, `SELECT s,pg_catalog.abs(s),pg_catalog.ceil(r),pg_catalog.ceiling(n),pg_catalog.factorial(s),pg_catalog.factorial(pg_catalog.abs(s)) FROM m`} {
		s := referenceSelect(query, []string{"s", "abs", "ceil", "ceiling", "factorial", "factorial"}, []string{"INT2", "INT2", "FLOAT8", "NUMERIC", "NUMERIC", "NUMERIC"}, [][]any{{int64(5), int64(5), float64(-1), "-5", "120", "120"}})
		p.step(t, s)
		p.step(t, referencePrepared(s))
	}
}
