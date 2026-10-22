package reference_test

import (
	"database/sql"
	"math"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceEarthValues(t *testing.T) {
	cases := []struct{ input, output string }{
		{"6378168", "(6378168)"}, {" ( 6378168 , 0 ) ", "(6378168, 0)"},
		{"(6378168,0,0)", "(6378168, 0, 0)"}, {"(-6378168,-0)", "(-6378168, -0)"},
		{"[(6378168,0),(6378168,0)]", "(6378168, 0)"},
		{"[(-6378168,-0),(-6378168,0)]", "(-6378168, -0)"},
		{"(6378168,NaN)", "(6378168, NaN)"}, {"(6378168,0,NaN)", "(6378168, 0, NaN)"},
		{"(6378168,0.10000000000000002)", "(6378168, 0.10000000000000002)"},
		{"(2126056,4252112,4252112)", "(2126056, 4252112, 4252112)"},
		{"6378162", "(6378162)"}, {"6378174", "(6378174)"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			earthScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				earthInstall(t, p, b, c)
				for _, prepared := range []bool{false, true} {
					for _, q := range []string{"SELECT " + cubeLiteral(tc.input) + "::earth AS v", "SELECT CAST(" + cubeLiteral(tc.input) + " AS earth) AS v", "SELECT $1::earth AS v"} {
						var args []any
						if q == "SELECT $1::earth AS v" {
							args = []any{tc.input}
						}
						s := earthSelect(b, q, []string{"v"}, [][]any{{tc.output}}, args...)
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					}
				}
			})
		})
	}
	// Expectations use the released binary64 expression, not a production helper
	// or PostgreSQL/native output. Freeze each adjacent accepted/rejected pair
	// before issuing SQL. sqrt(x*x) matters even on an axis.
	for _, lower := range []bool{true, false} {
		t.Run(map[bool]string{true: "lower_adjacent", false: "upper_adjacent"}[lower], func(t *testing.T) {
			const radius = 6378168.0
			accepts := func(x float64) bool { return math.Abs(math.Sqrt(x*x)/radius-1) < 1e-6 }
			x, direction := radius*(1+1e-6), math.Inf(1)
			if lower {
				x, direction = radius*(1-1e-6), math.Inf(-1)
			}
			for !accepts(x) {
				x = math.Nextafter(x, radius)
			}
			for accepts(math.Nextafter(x, direction)) {
				x = math.Nextafter(x, direction)
			}
			inside, outside := strconv.FormatFloat(x, 'g', -1, 64), strconv.FormatFloat(math.Nextafter(x, direction), 'g', -1, 64)
			// Output formatting is an independent fixed oracle expectation, not
			// Go's exponent-selection policy used to spell the input parameter.
			canonical := "(6378174.378168)"
			if lower {
				canonical = "(6378161.621832001)"
			}
			earthScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				earthInstall(t, p, b, c)
				for _, prepared := range []bool{false, true} {
					s := earthSelect(b, "SELECT $1::earth AS v", []string{"v"}, [][]any{{canonical}}, inside)
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					want := cubeParameterDiagnostic(earthCheckDiagnostic("public", "on_surface", true), outside, 1)
					cubeError(t, p, b, c, "SELECT $1::earth AS v", prepared, true, false, want, outside)
				}
			})
		})
	}
}
