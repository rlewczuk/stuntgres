//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceRandomFailuresPreserveState(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE signatures(f DOUBLE PRECISION,s SMALLINT)", 0))
	cases := []struct{ q, message string }{
		{"SELECT setseed(1.23456789)", "setseed parameter 1.23457 is out of allowed range [-1,1]"},
		{"SELECT setseed(1e20)", "setseed parameter 1e+20 is out of allowed range [-1,1]"},
		{"SELECT setseed(-1.0000000000000002)", "setseed parameter -1 is out of allowed range [-1,1]"},
		{"SELECT setseed(0.00000123456789e20)", "setseed parameter 1.23457e+14 is out of allowed range [-1,1]"},
		{"SELECT setseed('NaN')", "setseed parameter NaN is out of allowed range [-1,1]"},
		{"SELECT setseed('Infinity')", "setseed parameter Infinity is out of allowed range [-1,1]"},
		{"SELECT setseed('-Infinity')", "setseed parameter -Infinity is out of allowed range [-1,1]"},
		{"SELECT random(2,1)", "lower bound must be less than or equal to upper bound"},
		{"SELECT random(2147483649,2147483648)", "lower bound must be less than or equal to upper bound"},
		{"SELECT random(2.0,1.0)", "lower bound must be less than or equal to upper bound"},
		{"SELECT random(COALESCE('NaN',1.0),COALESCE('Infinity',1.0))", "lower bound cannot be NaN"},
		{"SELECT random(COALESCE('Infinity',1.0),COALESCE('NaN',1.0))", "lower bound cannot be infinity"},
		{"SELECT random('-Infinity',0.0)", "lower bound cannot be infinity"},
		{"SELECT random(0.0,'NaN')", "upper bound cannot be NaN"},
		{"SELECT random(0.0,'Infinity')", "upper bound cannot be infinity"},
		{"SELECT random(0.0,'-Infinity')", "upper bound cannot be infinity"},
	}
	for _, backend := range []struct {
		name string
		db   *sql.DB
	}{{"postgres", p.postgres}, {"native", p.memory}} {
		t.Run(backend.name, func(t *testing.T) {
			c, e := backend.db.Conn(p.ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close()
			seed := func() {
				t.Helper()
				if _, e := c.ExecContext(p.ctx, "SELECT setseed(0.25)"); e != nil {
					t.Fatal(e)
				}
			}
			draw := func() float64 {
				t.Helper()
				var n float64
				if e := c.QueryRowContext(p.ctx, "SELECT random()").Scan(&n); e != nil {
					t.Fatal(e)
				}
				return n
			}
			seed()
			expected := draw()
			for _, tc := range cases {
				for _, prepared := range []bool{false, true} {
					seed()
					var e error
					if prepared {
						s, err := c.PrepareContext(p.ctx, tc.q)
						if err != nil {
							t.Fatal("runtime validation during prepare", err)
						}
						_, e = s.ExecContext(p.ctx)
						s.Close()
					} else {
						_, e = c.ExecContext(p.ctx, tc.q)
					}
					want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: tc.message}
					referenceAssertDiagnostic(t, e, &want)
					if draw() != expected {
						t.Fatal("invalid call consumed or reseeded state", tc.q)
					}
				}
			}
		})
	}
	for _, q := range []string{"SELECT random(NULL,COALESCE('NaN',1.0))", "SELECT random(COALESCE('Infinity',1.0),NULL)", "SELECT random_normal(NULL,'Infinity')"} {
		s := referenceSelect(q, []string{"random"}, []string{"NUMERIC"}, [][]any{{nil}})
		if q == "SELECT random_normal(NULL,'Infinity')" {
			s.columns = []string{"random_normal"}
			s.types = []string{"FLOAT8"}
		}
		p.step(t, s)
		p.step(t, referencePrepared(s))
	}
	for _, q := range []string{"SELECT random(s,s) FROM signatures", "SELECT random($1,$2)"} {
		message := "function random(smallint, smallint) is not unique"
		if q == "SELECT random($1,$2)" {
			message = "function random(unknown, unknown) is not unique"
		}
		s := referenceImplicitError(q, referenceDiagnostic{Code: "42725", Message: message, Position: "8", Hint: "Could not choose a best candidate function. You might need to add explicit type casts."})
		s.selectRows = true
		s.errorStage = "prepare"
		p.step(t, referencePrepared(s))
	}
}
