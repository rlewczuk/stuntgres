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

func TestIntervalISOHexadecimal(t *testing.T) {
	d := testTypes()[Interval]
	for _, tc := range []struct {
		number string
		us     int64
	}{
		{"0x1p0", 1000000}, {"-0x1.8p1", -3000000},
		{"0x1.8", 1500000}, {"0x.8p0", 500000},
		{"0X1P0", 1000000}, {"0X1.8P+1", 3000000},
		{"0x1", 1000000}, {"0xep0", 14000000},
		{"0x.8", 500000}, {"-0x.8p0", -500000},
		{"0x1.p0", 1000000}, {"0x1.", 1000000},
		{"0x1p-1074", 0}, {"0x0p-4000", 0}, {"0x0p-99999", 0},
	} {
		for _, place := range []struct {
			prefix string
			days   int32
		}{{"PT", 0}, {"P1DT", 1}} {
			input := place.prefix + tc.number + "S"
			t.Run(input, func(t *testing.T) {
				v, err := d.Input(context.Background(), input, -1)
				if err != nil {
					t.Fatal(err)
				}
				months, days, us, err := v.Interval()
				if err != nil || months != 0 || days != place.days || us != tc.us {
					t.Fatal(months, days, us, err)
				}
			})
		}
	}
	for _, tc := range []struct {
		input        string
		months, days int32
		us           int64
	}{
		{"P0x1p0Y", 12, 0, 0}, {"P0x1p0M", 1, 0, 0}, {"P0x1p0D", 0, 1, 0},
		{"P1Y0x1p0M", 13, 0, 0}, {"P0x1p0", 12, 0, 0}, {"PT0x1p0", 0, 0, 3600000000},
		{"P1-0x1p0-1", 13, 1, 0}, {"P1-1-0x1p0", 13, 1, 0},
		{"PT1:0x1p0:1", 0, 0, 3661000000}, {"PT1:1:0x1p0", 0, 0, 3661000000},
	} {
		t.Run(tc.input, func(t *testing.T) {
			v, err := d.Input(context.Background(), tc.input, -1)
			if err != nil {
				t.Fatal(err)
			}
			months, days, us, err := v.Interval()
			if err != nil || months != tc.months || days != tc.days || us != tc.us {
				t.Fatal(months, days, us, err)
			}
		})
	}
}

func TestIntervalISONonfinite(t *testing.T) {
	d := testTypes()[Interval]
	for _, number := range []string{"-NaN", "-Inf", "-Infinity", "-NaN(foo)", "-nan", "-INF", "-nan(bad"} {
		for _, prefix := range []string{"PT", "P1DT"} {
			input := prefix + number + "S"
			t.Run(input, func(t *testing.T) {
				_, err := d.Input(context.Background(), input, -1)
				intervalBoundaryError(t, err, "22015", "interval field value out of range: \""+input+"\"")
			})
		}
	}
	for _, input := range []string{"PTNaNS", "PTInfS"} {
		t.Run(input, func(t *testing.T) {
			_, err := d.Input(context.Background(), input, -1)
			intervalBoundaryError(t, err, "22007", "invalid input syntax for type interval: \""+input+"\"")
		})
	}
}

func TestIntervalISOHexadecimalDiagnostics(t *testing.T) {
	d := testTypes()[Interval]
	for _, tc := range []struct{ number, code, primary string }{
		{"0x1p", "22007", "invalid input syntax for type interval"},
		{"0x1p+", "22007", "invalid input syntax for type interval"},
		{"0x", "22007", "invalid input syntax for type interval"},
		{"0x.p0", "22007", "invalid input syntax for type interval"},
		{"+0x1p0", "22007", "invalid input syntax for type interval"},
		{"0x1p1024", "22007", "invalid input syntax for type interval"},
		{"0x1p-1075", "22007", "invalid input syntax for type interval"},
		{"0x1.8p-1074", "22007", "invalid input syntax for type interval"},
		{"0x1p50", "22015", "interval field value out of range"},
		{"0x1p49", "22015", "interval field value out of range"},
		{"1000000000000001e+", "22015", "interval field value out of range"},
	} {
		for _, prefix := range []string{"PT", "P1DT"} {
			input := prefix + tc.number + "S"
			t.Run(input, func(t *testing.T) {
				_, err := d.Input(context.Background(), input, -1)
				intervalBoundaryError(t, err, tc.code, tc.primary+": \""+input+"\"")
			})
		}
	}
}

func TestIntervalISONumberConsumedWidth(t *testing.T) {
	for _, tc := range []struct {
		raw         string
		whole       int64
		fraction    float64
		used, width int
	}{
		{"0x1p0S", 1, 0, 5, 1}, {"0x1.8S", 1, .5, 5, 1},
		{"0x.8p0S", 0, .5, 6, 1}, {"-0X1.8P+1S", -3, 0, 9, 1},
		{"0x1p+S", 1, 0, 3, 1}, {"-0x1pS", -1, 0, 4, 1},
		{"0x.p0S", 0, 0, 1, 1}, {"0xS", 0, 0, 1, 1},
		{"1e+S", 1, 0, 1, 1}, {"1.eS", 1, 0, 2, 1},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			whole, fraction, used, ok, overflow := isoNumber(tc.raw)
			if !ok || overflow || whole != tc.whole || fraction != tc.fraction || used != tc.used || isoIntegerWidth(tc.raw) != tc.width {
				t.Fatal(whole, fraction, used, ok, overflow, isoIntegerWidth(tc.raw))
			}
		})
	}
}

func TestIntervalISOHexadecimalOrdinaryAllocations(t *testing.T) {
	allocations := testing.AllocsPerRun(1000, func() {
		for _, input := range []string{"0x1p0S", "0x1.8S"} {
			whole, _, _, ok, overflow := isoNumber(input)
			if !ok || overflow || whole != 1 {
				panic("normal hexadecimal number rejected")
			}
		}
	})
	if allocations != 0 {
		t.Fatal("normal hexadecimal allocations", allocations)
	}
}
