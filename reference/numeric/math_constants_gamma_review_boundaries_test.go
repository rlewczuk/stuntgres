package reference_test

import (
	"math"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceMathConstantsGammaReviewBoundaries(t *testing.T) {
	p := fixture.NewScenario(t)
	// Independent 110-digit decimal Stirling/reflection on the exact represented
	// inputs, not native or oracle output. Tiny-negative magnitudes use 1/x:
	// Gamma(x)-1/x tends to -EulerGamma, negligible at this binary64 scale.
	// The frontier pair is adjacent binary64 and straddles half-minsubnormal.
	matrix := []struct {
		name, input, category string
		inputBits             uint64
		want                  float64
		ulps                  uint64
	}{
		{"branch33_below", "-33.00000000000001", "value", 0xc040800000000001, 1.620780150324401e-23, 2},
		{"branch33_above", "-32.99999999999999", "value", 0xc0407fffffffffff, -1.6207801503244818e-23, 2},
		{"pole64_below", "-64.00000000000001", "value", 0xc050000000000001, -5.545783396715156e-76, 2},
		{"pole64_above", "-63.99999999999999", "value", 0xc04fffffffffffff, 1.1091566793431296e-75, 2},
		{"pole128_below", "-128.00000000000003", "value", 0xc060000000000001, -9.124093168823167e-203, 2},
		{"pole128_above", "-127.99999999999999", "value", 0xc05fffffffffffff, 1.824818633765011e-202, 2},
		{"cutoff200_below", "-200.00000000000003", "underflow", 0xc069000000000001, 0, 0},
		{"cutoff200_above", "-199.99999999999997", "underflow", 0xc068ffffffffffff, 0, 0},
		{"normal34_quarter", "-34.25", "value", 0xc041200000000000, -6.203678516204273e-39, 2},
		{"normal40_three_quarters", "-40.75", "value", 0xc044600000000000, -3.368394306209025e-49, 2},
		{"normal63_quarter", "-63.25", "value", 0xc04fa00000000000, 7.934507056264674e-88, 2},
		{"normal64_three_quarters", "-64.75", "value", 0xc050300000000000, -1.5317522760105228e-90, 2},
		{"normal127_quarter", "-127.25", "value", 0xc05fd00000000000, 4.387635558714902e-214, 2},
		{"normal128_three_quarters", "-128.75", "value", 0xc060180000000000, -3.0121591244388164e-217, 2},
		{"zero_frontier_nonzero", "-177.7807064574756", "value", 0xc06638fb8c1bd447, math.Float64frombits(1), 0},
		{"zero_frontier_zero", "-177.78070645747562", "underflow", 0xc06638fb8c1bd448, 0, 0},
		{"tiny_negative_overflow", "-5.562684646268003e-309", "overflow", 0x8004000000000000, 0, 0},
		{"tiny_negative_finite_neighbor", "-5.56268464626801e-309", "value", 0x8004000000000001, -1.7976931348623143e308, 2},
		{"tiny_negative_normal", "-1e-308", "value", 0x800730d67819e8d2, -1e308, 2},
		{"tiny_negative_minimum", "-5e-324", "overflow", 0x8000000000000001, 0, 0},
	}
	// Assert decimal SQL input identity independently before gamma evaluation.
	referenceMathTrigBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		for _, tc := range matrix {
			t.Run(tc.name+"/input_identity", func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					referenceScalarFloatQuery(t, p.Ctx, backend.DB, backend.Recorder, referenceScalarFloatCase{tc.name, "SELECT '" + tc.input + "'::float8 AS result", "FLOAT8", math.Float64frombits(tc.inputBits), 0}, prepared)
				}
			})
		}
	})
	cases := make([]referenceMathConstantsProbe, len(matrix))
	for i, tc := range matrix {
		cases[i] = referenceMathConstantsProbe{name: tc.name, function: "gamma", input: tc.input, category: tc.category, want: tc.want, ulps: tc.ulps}
		if tc.name == "normal128_three_quarters" {
			// Root's pinned PG18 tests-8 observed 0x92fa951a80fb728b:
			// three ULP from the independent mathematical target ...7288.
			// Preserve that mathematical native/private target and the same
			// two-ULP tolerance; this one PG witness uses its recorded target.
			cases[i].pgWantBits = 0x92fa951a80fb728b
		}
	}
	// Shared complete literal/prefix/held/SQL parameter and constant routes.
	referenceMathConstantsGammaCases(t, p, cases)
}
