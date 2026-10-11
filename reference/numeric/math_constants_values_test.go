package reference_test

import (
	"math"
	"testing"

	"stuntgres/reference/fixture"
)

var referenceMathConstantsUnaryNames = []string{"cbrt", "degrees", "radians", "erf", "erfc", "gamma", "exp"}

func TestReferenceMathConstantsValues(t *testing.T) {
	p := fixture.NewScenario(t)
	// Manual examples and independently rounded mathematical constants. Two ULP
	// permits the manual's finite libm last-bit variance, never a sign change.
	cases := []referenceMathConstantsProbe{
		{name: "positive_cube", function: "cbrt", input: "64", category: "value", want: 4, ulps: 2},
		{name: "negative_cube", function: "cbrt", input: "-8", category: "value", want: -2, ulps: 2},
		{name: "degrees_half", function: "degrees", input: "0.5", category: "value", want: 28.64788975654116, ulps: 2},
		{name: "radians_45", function: "radians", input: "45", category: "value", want: .7853981633974483, ulps: 2},
		{name: "erf_one", function: "erf", input: "1", category: "value", want: .8427007929497149, ulps: 2},
		{name: "erfc_one", function: "erfc", input: "1", category: "value", want: .15729920705028513, ulps: 2},
		{name: "erfc_cancellation_tail", function: "erfc", input: "10", category: "value", want: 2.088487583762545e-45, ulps: 2},
		{name: "gamma_half", function: "gamma", input: "0.5", category: "value", want: 1.772453850905516, ulps: 2},
		{name: "gamma_six", function: "gamma", input: "6", category: "value", want: 120, ulps: 2},
		{name: "gamma_negative_half", function: "gamma", input: "-0.5", category: "value", want: -3.544907701811032, ulps: 2},
		{name: "exp_one", function: "exp", input: "1", category: "value", want: 2.718281828459045, ulps: 2},
	}
	referenceMathTrigBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) { referenceMathConstantsProbeModes(t, p, backend, tc) })
		}
		for _, prepared := range []bool{false, true} {
			referenceScalarFloatQuery(t, p.Ctx, backend.DB, backend.Recorder, referenceScalarFloatCase{"pi", "SELECT pi() AS result", "FLOAT8", 3.141592653589793, 0}, prepared)
		}
	})
}

func TestReferenceMathConstantsIEEE(t *testing.T) {
	p := fixture.NewScenario(t)
	referenceMathTrigBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		for _, name := range referenceMathConstantsUnaryNames {
			for _, input := range []string{"0", "-0", "NaN", "Infinity", "-Infinity"} {
				t.Run(name+"/"+input, func(t *testing.T) {
					tc := referenceMathConstantsProbe{name: name + input, function: name, input: input, category: "value"}
					switch input {
					case "-0":
						tc.want = math.Copysign(0, -1)
					case "NaN":
						tc.want = math.NaN()
					case "Infinity":
						tc.want = math.Inf(1)
					case "-Infinity":
						tc.want = math.Inf(-1)
					}
					switch name {
					case "erf":
						if input == "Infinity" {
							tc.want = 1
						}
						if input == "-Infinity" {
							tc.want = -1
						}
					case "erfc":
						if input == "0" || input == "-0" {
							tc.want = 1
						}
						if input == "Infinity" {
							tc.want = 0
						}
						if input == "-Infinity" {
							tc.want = 2
						}
					case "gamma":
						if input == "0" || input == "-0" || input == "-Infinity" {
							tc.category = "overflow"
						}
					case "exp":
						if input == "0" || input == "-0" {
							tc.want = 1
						}
						if input == "-Infinity" {
							tc.want = 0
						}
					}
					referenceMathConstantsProbeModes(t, p, backend, tc)
				})
			}
			for _, arg := range []string{"NULL", "NULL::float8"} {
				s := fixture.Select("SELECT "+name+"("+arg+") AS result", []string{"result"}, []string{"FLOAT8"}, [][]any{{nil}})
				referenceMathTrigStep(t, p, backend, s)
			}
		}
	})
}
