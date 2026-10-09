package reference_test

import (
	"database/sql"
	"math"
	"testing"
)

func TestReferenceRandomFailuresPreserveState(t *testing.T) {
	p := newReferenceScenario(t)

	cases := []struct{ q, message string }{
		{"SELECT setseed(1.23456789)", "setseed parameter 1.23457 is out of allowed range [-1,1]"},
		{"SELECT setseed(1e20)", "setseed parameter 1e+20 is out of allowed range [-1,1]"},
		{"SELECT setseed(-1.0000000000000002)", "setseed parameter -1 is out of allowed range [-1,1]"},
		{"SELECT setseed(0.00000123456789e20)", "setseed parameter 1.23457e+14 is out of allowed range [-1,1]"},
		{"SELECT setseed('NaN')", "setseed parameter NaN is out of allowed range [-1,1]"},
		{"SELECT setseed('Infinity')", "setseed parameter Infinity is out of allowed range [-1,1]"},
		{"SELECT setseed('-Infinity')", "setseed parameter -Infinity is out of allowed range [-1,1]"},
		{"SELECT random('NaN',1.0)", "lower bound cannot be NaN"},
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
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			c, e := backend.db.Conn(p.ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close()
			draw := func() float64 {
				var value float64
				values := randomRead(t, p.ctx, c, backend, "SELECT random()", []string{"random"}, []string{"FLOAT8"}, func(t *testing.T, rows *sql.Rows, rowIndex int) {
					if err := rows.Scan(&value); err != nil {
						t.Fatal("diagnostic continuation float64 scan", err)
					}
					if rowIndex != 0 {
						t.Fatal("diagnostic continuation extra row", rowIndex)
					}
				})
				if len(values) != 1 || values[0][0] != value {
					t.Fatal("diagnostic continuation typed/raw mismatch", values, value)
				}
				if math.IsNaN(value) || value < 0 || value >= 1 {
					t.Fatal("diagnostic continuation range", value)
				}
				return value
			}
			randomReseed(t, p, c, backend, .25)
			expected := draw()
			for _, tc := range cases {
				for _, prepared := range []bool{false, true} {
					for _, query := range []bool{false, true} {
						randomReseed(t, p, c, backend, .25)
						step := referenceImplicitError(tc.q, referenceDiagnostic{Code: "22023", Message: tc.message})
						step.prepared = prepared
						step.selectRows = query
						step.errorStage = "execute"
						referenceRunStep(t, p.ctx, c, backend.recorder, step)
						if draw() != expected {
							t.Fatal("invalid call consumed state", tc.q)
						}
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
}
