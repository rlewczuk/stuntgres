package sqltype

import (
	"context"
	"errors"
	"math"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestIntervalComponents(t *testing.T) {
	d := testTypes()[Interval]
	ctx := context.Background()
	for _, tc := range []struct {
		s       string
		m, days int32
		us      int64
		out     string
	}{
		{"2 years 15 months 100 weeks 99 hours 123456789 milliseconds", 39, 700, 479856789000, "3 years 3 mons 700 days 133:17:36.789"},
		{"1.75 months", 1, 22, 43200000000, "1 mon 22 days 12:00:00"}, {"1.5 weeks", 0, 10, 43200000000, "10 days 12:00:00"},
		{"1-2", 14, 0, 0, "1 year 2 mons"}, {"3 4:05:06", 0, 3, 14706000000, "3 days 04:05:06"},
		{"P2Y10M15DT10H30M20S", 34, 15, 37820000000, "2 years 10 mons 15 days 10:30:20"},
		{"P00021015T103020", 34, 15, 37820000000, "2 years 10 mons 15 days 10:30:20"},
		{"P0002-10-15T10:30:20", 34, 15, 37820000000, "2 years 10 mons 15 days 10:30:20"},
		{"P1Y2Y", 36, 0, 0, "3 years"}, {"P2", 24, 0, 0, "2 years"}, {"P2-10", 34, 0, 0, "2 years 10 mons"}, {"PT10:30", 0, 0, 37800000000, "10:30:00"},
		{"24 hours", 0, 0, 86400000000, "24:00:00"}, {"1 day", 0, 1, 0, "1 day"}, {"30 days", 0, 30, 0, "30 days"},
		{"@ 1 year 2 mons -3 days 4 hours 5 mins 6 secs ago", -14, 3, -14706000000, "-1 years -2 mons +3 days -04:05:06"},
		{"1 second 2 milliseconds 3 microseconds", 0, 0, 1002003, "00:00:01.002003"},
		{"0.0000005 seconds", 0, 0, 0, "00:00:00"}, {"00:00:00.0000015", 0, 0, 2, "00:00:00.000002"},
		{"2147483647 months", math.MaxInt32, 0, 0, "178956970 years 7 mons"},
		{"-2147483648 days", 0, math.MinInt32, 0, "-2147483648 days"},
		{"-9223372036854775808 microseconds", 0, 0, math.MinInt64, "-2562047788:00:54.775808"},
	} {
		t.Run(tc.s, func(t *testing.T) {
			v, e := d.Input(ctx, tc.s, -1)
			if e != nil {
				t.Fatal(e)
			}
			m, days, us, e := v.Interval()
			if e != nil || m != tc.m || days != tc.days || us != tc.us {
				t.Fatal(m, days, us, e)
			}
			out, e := d.Output(ctx, v)
			if e != nil || out != tc.out {
				t.Fatal(out, e, tc.out)
			}
		})
	}
}
func TestIntervalModifiers(t *testing.T) {
	d := testTypes()[Interval]
	ctx := context.Background()
	for _, r := range intervalRanges {
		for _, precision := range []int32{0, 1, 2, 3, 4, 5, 6, IntervalFullPrecision} {
			mods := []int32{r.mask}
			if precision != IntervalFullPrecision {
				mods = append(mods, precision)
			}
			u, w, e := d.ResolveModifiers(mods...)
			if e != nil || w != nil || !u.Valid() {
				t.Fatal(r, precision, u, w, e)
			}
			declaration, e := u.Declaration()
			if e != nil || declaration == "" {
				t.Fatal(declaration, e)
			}
			v, e := d.Input(ctx, "13 months 2 days 3:04:05.6789", u.Typmod)
			if e != nil {
				t.Fatal(e)
			}
			out, e := d.Output(ctx, v)
			if e != nil {
				t.Fatal(e)
			}
			if precision == 2 {
				want := "1 year 1 mon 2 days 03:04:05.68"
				switch r.mask {
				case IntervalYear:
					want = "1 year"
				case IntervalMonth, IntervalYear | IntervalMonth:
					want = "1 year 1 mon"
				case IntervalDay:
					want = "1 year 1 mon 2 days"
				default:
					if r.mask&IntervalSecond == 0 {
						if r.mask&IntervalMinute != 0 {
							want = "1 year 1 mon 2 days 03:04:00"
						} else {
							want = "1 year 1 mon 2 days 03:00:00"
						}
					}
				}
				if out != want {
					t.Fatal(declaration, out, want)
				}
			}
		}
	}
	u, w, e := d.ResolveModifiers(IntervalFullRange, 7)
	if e != nil || u.Typmod != IntervalFullRange<<16|6 || w == nil || w.Message != "INTERVAL(7) precision reduced to maximum allowed, 6" {
		t.Fatal(u, w, e)
	}
	for _, tc := range []struct {
		r           int32
		input, want string
	}{{IntervalYear, "1", "1 year"}, {IntervalYear | IntervalMonth, "1", "1 mon"}, {IntervalDay | IntervalHour | IntervalMinute | IntervalSecond, "1", "00:00:01"}, {IntervalMinute | IntervalSecond, "1:02.345", "00:01:02.35"}, {IntervalSecond, "-1.235 seconds", "-00:00:01.24"}, {IntervalSecond, "59.9996 seconds", "00:01:00"}} {
		p := int32(2)
		if tc.input == "59.9996 seconds" {
			p = 3
		}
		u, _, _ := d.ResolveModifiers(tc.r, p)
		v, e := d.Input(ctx, tc.input, u.Typmod)
		if e != nil {
			t.Fatal(e)
		}
		out, e := d.Output(ctx, v)
		if e != nil || out != tc.want {
			t.Fatal(tc.input, out, e)
		}
	}
}
func TestIntervalStylesAndCanonical(t *testing.T) {
	d := testTypes()[Interval]
	ctx := context.Background()
	v, _ := IntervalValue(d, -14, 3, -14706000000)
	outputs := []string{"-1 years -2 mons +3 days -04:05:06", "@ 1 year 2 mons -3 days 4 hours 5 mins 6 secs ago", "-1-2 +3 -4:05:06", "P-1Y-2M3DT-4H-5M-6S"}
	zeros := []string{"00:00:00", "@ 0", "0", "PT0S"}
	for style := IntervalPostgres; style <= IntervalISO8601; style++ {
		out, e := d.OutputWithEnvironment(ctx, v, OutputEnvironment{IntervalStyle: style})
		if e != nil || out != outputs[style] {
			t.Fatal(style, out, e)
		}
		zero, _ := IntervalValue(d, 0, 0, 0)
		out, e = d.OutputWithEnvironment(ctx, zero, OutputEnvironment{IntervalStyle: style})
		if e != nil || out != zeros[style] {
			t.Fatal(style, out, e)
		}
		for _, input := range []string{intervalCanonical(v), "infinity", "-infinity"} {
			got, e := d.InputWithEnvironment(ctx, input, -1, InputEnvironment{IntervalStyle: style})
			if e != nil {
				t.Fatal(e)
			}
			if input == intervalCanonical(v) && !SameDatum(got, v) {
				t.Fatal("canonical components changed", style)
			}
		}
		got, e := d.InputWithEnvironment(ctx, "-1 2:03:04", -1, InputEnvironment{IntervalStyle: style})
		if e != nil {
			t.Fatal(e)
		}
		want := int64(7384000000)
		if style == IntervalSQLStandard {
			want = -want
		}
		if got.days != -1 || got.integer != want {
			t.Fatal(style, got)
		}
	}
}
func TestIntervalDiagnostics(t *testing.T) {
	d := testTypes()[Interval]
	for _, tc := range []struct{ s, code, message string }{
		{"invalid", "22007", "invalid input syntax for type interval: \"invalid\""}, {"1 second 2 seconds", "22007", "invalid input syntax for type interval: \"1 second 2 seconds\""},
		{"5.5 seconds 3 milliseconds", "22007", "invalid input syntax for type interval: \"5.5 seconds 3 milliseconds\""},
		{"2147483648 days", "22015", "interval field value out of range: \"2147483648 days\""}, {"9223372036854775808 microseconds", "22015", "interval field value out of range: \"9223372036854775808 microseconds\""},
		{"2147483647 years", "22008", "interval out of range"}, {"1-12", "22015", "interval field value out of range: \"1-12\""},
		{"P", "22007", "invalid input syntax for type interval: \"P\""},
	} {
		_, e := d.Input(context.Background(), tc.s, -1)
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message}
		if !errors.As(e, &got) || *got != want {
			t.Fatal(tc.s, e, want)
		}
	}
	u, _, _ := d.ResolveModifiers(IntervalSecond, 2)
	_, e := d.Input(context.Background(), "2562047788:00:54.775807", u.Typmod)
	var got *sqlstate.Error
	if !errors.As(e, &got) || got.Code != "22008" || got.Message != "interval out of range" {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = d.Input(ctx, "1 day", -1); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
func TestIntervalWidenedComparisonGroupingAndExactDatum(t *testing.T) {
	d := testTypes()[Interval]
	ctx := context.Background()
	cmp, e := BindComparison(d, d, CollationRef{}, false)
	if e != nil {
		t.Fatal(e)
	}
	group, e := BindGrouping(d.Usage())
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range [][2]string{{"1 month", "30 days"}, {"1 day", "24 hours"}, {"2147483647 months -2147483648 days", "2147483646 months -2147483618 days"}} {
		a, e := d.Input(ctx, tc[0], -1)
		if e != nil {
			t.Fatal(e)
		}
		b, e := d.Input(ctx, tc[1], -1)
		if e != nil {
			t.Fatal(e)
		}
		n, e := cmp.Compare(ctx, a, b, InputEnvironment{})
		if e != nil || n != 0 || SameDatum(a, b) {
			t.Fatal(tc, n, e)
		}
		ha, e := group.Hash(ctx, a)
		if e != nil {
			t.Fatal(e)
		}
		hb, e := group.Hash(ctx, b)
		if e != nil || ha != hb {
			t.Fatal(tc, ha, hb, e)
		}
	}
	low, _ := d.Input(ctx, "-infinity", -1)
	high, _ := d.Input(ctx, "infinity", -1)
	finite, _ := IntervalValue(d, math.MaxInt32, math.MaxInt32, math.MinInt64)
	if n, _ := cmp.Compare(ctx, low, finite, InputEnvironment{}); n != -1 {
		t.Fatal(n)
	}
	if n, _ := cmp.Compare(ctx, finite, high, InputEnvironment{}); n != -1 {
		t.Fatal(n)
	}
}
