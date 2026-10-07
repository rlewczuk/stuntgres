//go:build reference

package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"
)

func TestReferenceRandomDeterministicResults(t *testing.T) {
	p := newReferencePair(t)
	for _, tc := range []struct {
		q, name, typ string
		value        any
	}{
		{"SELECT random(7,7)", "random", "INT4", int64(7)},
		{"SELECT random(2147483648,2147483648)", "random", "INT8", int64(2147483648)},
		{"SELECT random(1.0,1.000)", "random", "NUMERIC", "1.000"},
		{"SELECT random(0.00,0.000)", "random", "NUMERIC", "0.000"},
		{"SELECT random_normal(5,0)", "random_normal", "FLOAT8", float64(5)},
		{"SELECT random_normal(NULL,1)", "random_normal", "FLOAT8", nil},
	} {
		s := referenceSelect(tc.q, []string{tc.name}, []string{tc.typ}, [][]any{{tc.value}})
		p.step(t, s)
		p.step(t, referencePrepared(s))
	}
}

// Values are checked against independent ranges and each backend's own reseed
// witness. No random draws are compared between PostgreSQL and native.
func TestReferenceRandomStreams(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []struct {
		name string
		db   *sql.DB
	}{{"postgres", p.postgres}, {"native", p.memory}} {
		t.Run(backend.name, func(t *testing.T) {
			c, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			one := func(q string) any {
				t.Helper()
				var v any
				if err := c.QueryRowContext(p.ctx, q).Scan(&v); err != nil {
					t.Fatal(q, err)
				}
				return v
			}
			seed := func() {
				t.Helper()
				if _, err := c.ExecContext(p.ctx, "SELECT setseed(0.25)"); err != nil {
					t.Fatal(err)
				}
			}
			series := func() []any {
				t.Helper()
				return []any{one("SELECT random()"), one("SELECT random(-7,3)"), one("SELECT random(2147483648,2147483650)"), one("SELECT random(100000000000000000000.000,100000000000000000000.002)"), one("SELECT random_normal()"), one("SELECT random_normal(5)"), one("SELECT random_normal(5,2)")}
			}
			seed()
			a := series()
			x, ok := a[0].(float64)
			if !ok || math.IsNaN(x) || x < 0 || x >= 1 {
				t.Fatal(a)
			}
			n, ok := a[1].(int64)
			if !ok || n < -7 || n > 3 {
				t.Fatal(a)
			}
			n, ok = a[2].(int64)
			if !ok || n < 2147483648 || n > 2147483650 {
				t.Fatal(a)
			}
			seed()
			if b := series(); !reflect.DeepEqual(a, b) {
				t.Fatal("same-session reseed", a, b)
			}
			seed()
			implicit := one("SELECT random_normal()")
			seed()
			explicit := one("SELECT random_normal(0,1)")
			if implicit != explicit {
				t.Fatal("defaults", implicit, explicit)
			}
			seed()
			first := one("SELECT random()")
			second := one("SELECT random()")
			seed()
			if one("SELECT random_normal(NULL,random())") != nil {
				t.Fatal("strict NULL")
			}
			if one("SELECT random()") != first {
				t.Fatal("literal NULL sibling effect")
			}
			seed()
			if one("SELECT random_normal(NULL::int4,random())") != nil {
				t.Fatal("typed strict NULL")
			}
			if one("SELECT random()") != first {
				t.Fatal("typed literal NULL sibling effect")
			}
			seed()
			tx, err := c.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			var d float64
			if err = tx.QueryRowContext(p.ctx, "SELECT random()").Scan(&d); err != nil {
				t.Fatal(err)
			}
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			if one("SELECT random()") != second {
				t.Fatal("rollback rewound draw")
			}
		})
	}
}

func TestReferenceRandomDiagnostics(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE signatures(f DOUBLE PRECISION,s SMALLINT)", 0))
	for _, tc := range []struct{ q, message string }{
		{"SELECT setseed(1.23456789)", "setseed parameter 1.23457 is out of allowed range [-1,1]"},
		{"SELECT setseed('NaN')", "setseed parameter NaN is out of allowed range [-1,1]"},
		{"SELECT setseed('Infinity')", "setseed parameter Infinity is out of allowed range [-1,1]"},
		{"SELECT setseed('-Infinity')", "setseed parameter -Infinity is out of allowed range [-1,1]"},
		{"SELECT random(2,1)", "lower bound must be less than or equal to upper bound"},
		{"SELECT random(2147483649,2147483648)", "lower bound must be less than or equal to upper bound"},
		{"SELECT random(2.0,1.0)", "lower bound must be less than or equal to upper bound"},
		{"SELECT random('NaN',1.0)", "lower bound cannot be NaN"},
		{"SELECT random(0.0,'Infinity')", "upper bound cannot be infinity"},
	} {
		s := referenceImplicitError(tc.q, referenceDiagnostic{Code: "22023", Message: tc.message})
		s.selectRows = true
		s.errorStage = "execute"
		p.step(t, s)
		p.step(t, referencePrepared(s))
	}
	for _, tc := range []struct{ q, code, message, hint string }{
		{"SELECT random(f,f) FROM signatures", "42883", "function random(double precision, double precision) does not exist", "No function matches the given name and argument types. You might need to add explicit type casts."},
		{"SELECT random('1','1')", "42725", "function random(unknown, unknown) is not unique", "Could not choose a best candidate function. You might need to add explicit type casts."},
		{"SELECT random_normal(1,TRUE)", "42883", "function random_normal(integer, boolean) does not exist", "No function matches the given name and argument types. You might need to add explicit type casts."},
	} {
		s := referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.message, Hint: tc.hint, Position: "8"})
		s.selectRows = true
		s.errorStage = "execute"
		p.step(t, s)
		s.errorStage = "prepare"
		p.step(t, referencePrepared(s))
	}
}
