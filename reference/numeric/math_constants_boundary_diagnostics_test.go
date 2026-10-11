package reference_test

import (
	"math"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceMathConstantsBoundaryDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	// Release wrapper categories plus root's pinned D1 finite witnesses.
	// The manual permits last-bit libm variance; signs and endpoints stay exact.
	cases := []referenceMathConstantsProbe{
		{name: "radians_minimum", function: "radians", input: "5e-324", category: "underflow"},
		{name: "radians_minimum_neighbor", function: "radians", input: "1e-323", category: "underflow"},
		{name: "radians_1e_minus_322", function: "radians", input: "1e-322", category: "underflow"},
		{name: "radians_zero_frontier_below", function: "radians", input: "1.4e-322", category: "underflow"},
		{name: "radians_zero_frontier_above", function: "radians", input: "1.5e-322", category: "value", want: math.Float64frombits(1)},
		{name: "radians_subnormal_neighbor", function: "radians", input: "2e-322", category: "value", want: math.Float64frombits(1)},
		{name: "degrees_finite_overflow", function: "degrees", input: "1e308", category: "overflow"},
		{name: "erfc_subnormal_tail", function: "erfc", input: "27", category: "value", want: 5.2370464393526292e-319, ulps: 2},
		{name: "erfc_zero_regression", function: "erfc", input: "28", category: "value", want: 0},
		{name: "erfc_zero_tail", function: "erfc", input: "30", category: "value", want: 0},
		{name: "erf_negative_zero", function: "erf", input: "-0", category: "value", want: math.Copysign(0, -1)},
		{name: "cbrt_minimum_positive", function: "cbrt", input: "5e-324", category: "value", want: 1.7031839360032601e-108, ulps: 2},
		{name: "cbrt_minimum_negative", function: "cbrt", input: "-5e-324", category: "value", want: -1.7031839360032601e-108, ulps: 2},
		{name: "cbrt_maximum_positive", function: "cbrt", input: "1.7976931348623157e308", category: "value", want: 5.6438030941223623e102, ulps: 2},
		{name: "cbrt_maximum_negative", function: "cbrt", input: "-1.7976931348623157e308", category: "value", want: -5.6438030941223623e102, ulps: 2},
		// Existing independently pinned exp subnormal witnesses: do not infer
		// gamma's errno behavior from this accepted nonzero-subnormal policy.
		{name: "exp_two_subnormal_units", function: "exp", input: "-744", category: "value", want: math.Float64frombits(2)},
		{name: "exp_one_subnormal_unit", function: "exp", input: "-745", category: "value", want: math.Float64frombits(1)},
		{name: "exp_zero_underflow", function: "exp", input: "-746", category: "underflow"},
	}
	referenceMathTrigBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) { referenceMathConstantsProbeModes(t, p, backend, tc) })
		}
	})
}
