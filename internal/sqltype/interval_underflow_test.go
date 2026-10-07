package sqltype

import (
	"context"
	"math/big"
	"strconv"
	"strings"
	"testing"
)

// Construct exact decimal powers of two independently of interval decoding.
func intervalExactBinaryDecimal(numerator int64, shift uint) string {
	denominator := new(big.Int).Lsh(big.NewInt(1), shift)
	decimal := new(big.Rat).SetFrac(big.NewInt(numerator), denominator).FloatString(int(shift))
	digits := strings.TrimPrefix(decimal, "0.")
	first := 0
	for digits[first] == '0' {
		first++
	}
	significand := strings.TrimRight(digits[first:], "0")
	mantissa := significand[:1]
	if len(significand) > 1 {
		mantissa += "." + significand[1:]
	}
	return mantissa + "e-" + strconv.Itoa(first+1)
}

func TestIntervalISOUnderflow(t *testing.T) {
	d := testTypes()[Interval]
	cases := []struct {
		name, number string
		reject       bool
	}{
		{"zero_result", "1e-400", true},
		{"inexact_subnormal", "5e-324", true},
		{"exact_tiny", intervalExactBinaryDecimal(1, 1074), false},
		{"exact_normal", intervalExactBinaryDecimal(1, 1022), false},
		{"exact_largest_subnormal", intervalExactBinaryDecimal((1<<52)-1, 1074), false},
		{"normal_minus_half_tiny", intervalExactBinaryDecimal((1<<53)-1, 1075), true},
		{"normal_minus_quarter_tiny", intervalExactBinaryDecimal((1<<54)-1, 1076), false},
		{"normal_minus_threequarter_tiny", intervalExactBinaryDecimal((1<<54)-3, 1076), false},
		{"true_zero", "0e-400", false},
		{"small_normal", "1e-300", false},
	}
	for _, tc := range cases {
		tc.name = "negative/" + tc.name
		tc.number = "-" + tc.number
		cases = append(cases, tc)
	}
	for _, tc := range cases {
		for _, place := range []struct {
			name, prefix, suffix string
			months, days         int32
			us                   int64
		}{
			{"time_first", "PT", "S", 0, 0, 0}, {"time_later", "P1DT", "S", 0, 1, 0},
			{"date_first", "P", "Y", 0, 0, 0}, {"date_later", "P1Y", "M", 12, 0, 0},
			{"date_alternative_first", "P", "", 0, 0, 0}, {"time_alternative_first", "PT", "", 0, 0, 0},
			{"date_alternative_month", "P1-", "-1", 12, 1, 0}, {"date_alternative_day", "P1-1-", "", 13, 0, 0},
			{"time_alternative_minute", "PT1:", ":1", 0, 0, 3601000000}, {"time_alternative_second", "PT1:1:", "", 0, 0, 3660000000},
		} {
			t.Run(tc.name+"/"+place.name, func(t *testing.T) {
				input := place.prefix + tc.number + place.suffix
				v, err := d.Input(context.Background(), input, -1)
				if tc.reject {
					intervalBoundaryError(t, err, "22007", "invalid input syntax for type interval: \""+input+"\"")
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				months, days, us, err := v.Interval()
				if err != nil || months != place.months || days != place.days || us != place.us {
					t.Fatal(months, days, us, err)
				}
			})
		}
	}
}

func TestIntervalISOUnderflowOrdinaryAllocations(t *testing.T) {
	allocations := testing.AllocsPerRun(1000, func() {
		if isoNumberUnderflow("12e0", 12, true) || isoNumberUnderflow("0e-400", 0, false) {
			panic("ordinary ISO number rejected")
		}
	})
	if allocations != 0 {
		t.Fatal("ordinary ISO underflow classification allocated", allocations)
	}
}
