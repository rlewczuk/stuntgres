package reference_test

import (
	"database/sql"
	"math"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRandomFailuresPreserveState(t *testing.T) {
	p := fixture.NewScenario(t)

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
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c, e := backend.DB.Conn(p.Ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close()
			draw := func() float64 {
				var value float64
				values := randomRead(t, p.Ctx, c, backend, "SELECT random()", []string{"random"}, []string{"FLOAT8"}, func(t *testing.T, rows *sql.Rows, rowIndex int) {
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
						step := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "22023", Message: tc.message})
						step.Prepared = prepared
						step.SelectRows = query
						step.ErrorStage = "execute"
						fixture.RunStep(t, p.Ctx, c, backend.Recorder, step)
						if draw() != expected {
							t.Fatal("invalid call consumed state", tc.q)
						}
					}
				}
			}
		})
	}
	for _, q := range []string{"SELECT random(NULL,COALESCE('NaN',1.0))", "SELECT random(COALESCE('Infinity',1.0),NULL)", "SELECT random_normal(NULL,'Infinity')"} {
		s := fixture.Select(q, []string{"random"}, []string{"NUMERIC"}, [][]any{{nil}})
		if q == "SELECT random_normal(NULL,'Infinity')" {
			s.Columns = []string{"random_normal"}
			s.Types = []string{"FLOAT8"}
		}
		p.Step(t, s)
		p.Step(t, fixture.Prepared(s))
	}
}
