package sqltype

import (
	"context"
	"math"
	"testing"
)

func mathConstantsGammaReviewBoundaries(t *testing.T) {
	t.Helper()
	// Independent decimal targets on exact FLOAT8 inputs; see the permanent
	// public matrix for matching SQL/phase/diagnostic and input-identity proof.
	for _, tc := range []struct {
		input, category string
		inputBits       uint64
		want            float64
		ulps            uint64
	}{
		{"-33.00000000000001", "value", 0xc040800000000001, 1.620780150324401e-23, 2},
		{"-32.99999999999999", "value", 0xc0407fffffffffff, -1.6207801503244818e-23, 2},
		{"-64.00000000000001", "value", 0xc050000000000001, -5.545783396715156e-76, 2},
		{"-63.99999999999999", "value", 0xc04fffffffffffff, 1.1091566793431296e-75, 2},
		{"-128.00000000000003", "value", 0xc060000000000001, -9.124093168823167e-203, 2},
		{"-127.99999999999999", "value", 0xc05fffffffffffff, 1.824818633765011e-202, 2},
		{"-200.00000000000003", "underflow", 0xc069000000000001, 0, 0},
		{"-199.99999999999997", "underflow", 0xc068ffffffffffff, 0, 0},
		{"-34.25", "value", 0xc041200000000000, -6.203678516204273e-39, 2},
		{"-40.75", "value", 0xc044600000000000, -3.368394306209025e-49, 2},
		{"-63.25", "value", 0xc04fa00000000000, 7.934507056264674e-88, 2},
		{"-64.75", "value", 0xc050300000000000, -1.5317522760105228e-90, 2},
		{"-127.25", "value", 0xc05fd00000000000, 4.387635558714902e-214, 2},
		{"-128.75", "value", 0xc060180000000000, -3.0121591244388164e-217, 2},
		{"-177.7807064574756", "value", 0xc06638fb8c1bd447, math.Float64frombits(1), 0},
		{"-177.78070645747562", "underflow", 0xc06638fb8c1bd448, 0, 0},
		{"-5.562684646268003e-309", "overflow", 0x8004000000000000, 0, 0},
		{"-5.56268464626801e-309", "value", 0x8004000000000001, -1.7976931348623143e308, 2},
		{"-1e-308", "value", 0x800730d67819e8d2, -1e308, 2},
		{"-5e-324", "overflow", 0x8000000000000001, 0, 0},
	} {
		t.Run(tc.input, func(t *testing.T) {
			plan, values := scalarProvider(t, "gamma", Float8, tc.input)
			if math.Float64bits(values[0].floating) != tc.inputBits {
				t.Fatal("input identity", values[0])
			}
			got, err := plan.Evaluate(context.Background(), values[:plan.arity], InputEnvironment{})
			if tc.category != "value" {
				scalarError(t, err, "22003", "value out of range: "+tc.category)
				if got != (Value{}) {
					t.Fatal("error published datum", got)
				}
				return
			}
			if err != nil || got.IsNull() || got.Type() != plan.result.Type || !mathTrigMatches(got.floating, tc.want, tc.ulps) {
				t.Fatalf("gamma(%s) actual=%g bits=%016x want=%g bits=%016x error=%v", tc.input, got.floating, math.Float64bits(got.floating), tc.want, math.Float64bits(tc.want), err)
			}
		})
	}
}
