package sqltype

import (
	"context"
	"errors"
	"math"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestIntervalGenericDeclarationReparse(t *testing.T) {
	d := testTypes()[Interval]
	for _, mask := range []int32{32767, 4, 2, 8, 1024, 2048, 4096, 6, 1032, 3080, 7176, 3072, 7168, 6144} {
		for _, precision := range []int32{0, 1, 2, 3, 4, 5, 6, 65535} {
			inputMods := []int32{mask}
			if precision != 65535 {
				inputMods = append(inputMods, precision)
			}
			u, warning, err := d.ResolveModifiers(inputMods...)
			if err != nil || warning != nil {
				t.Fatal(err, warning)
			}
			declaration, err := u.Declaration()
			if err != nil {
				t.Fatal(err)
			}
			statements, err := parser.Parse("CREATE TABLE iv(v " + declaration + ")")
			if err != nil {
				t.Fatal(declaration, err)
			}
			raw := statements[0].(*parser.CreateTable).Elements[0].Column.TypeName
			mods := make([]int32, len(raw.Modifiers))
			for i, m := range raw.Modifiers {
				if m.Integer == nil {
					t.Fatal(raw)
				}
				mods[i] = *m.Integer
			}
			recovered := d.Usage()
			if len(mods) > 0 {
				recovered, warning, err = d.ResolveModifiers(mods...)
			}
			want := mask<<16 | precision
			if mask == 32767 && precision == 65535 {
				want = -1
			}
			if err != nil || warning != nil || u.Typmod != want || recovered.Typmod != want {
				t.Fatal(declaration, recovered, u, want, warning, err)
			}
		}
	}
}

func intervalBoundaryError(t *testing.T, err error, code, message string) {
	t.Helper()
	var got *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
	if !errors.As(err, &got) || *got != want {
		t.Fatal(err, want)
	}
}

func TestIntervalISOMagnitudeDiagnostics(t *testing.T) {
	d := testTypes()[Interval]
	for _, s := range []string{"PT1000000000000001S", "P1YT1000000000000001S", "PT-1000000000000001S", "P1000000000000001Y", "P1Y1000000000000001M", "P2-1000000000000001-1", "P2-1-1000000000000001", "PT1:1000000000000001:1", "PT1:1:1000000000000001"} {
		_, err := d.Input(context.Background(), s, -1)
		intervalBoundaryError(t, err, "22015", "interval field value out of range: \""+s+"\"")
	}
}

func TestIntervalYearMonthImplicitUnit(t *testing.T) {
	d := testTypes()[Interval]
	typmods := []int32{-1}
	for _, mask := range []int32{IntervalYear, IntervalYear | IntervalMonth, IntervalDay, IntervalMinute | IntervalSecond} {
		u, _, err := d.ResolveModifiers(mask)
		if err != nil {
			t.Fatal(err)
		}
		typmods = append(typmods, u.Typmod)
	}
	for _, typmod := range typmods {
		_, err := d.Input(context.Background(), "1 1-2", typmod)
		intervalBoundaryError(t, err, "22007", "invalid input syntax for type interval: \"1 1-2\"")
	}
}

func TestIntervalISOAlternativeExponentWidth(t *testing.T) {
	d := testTypes()[Interval]
	for _, tc := range []struct {
		s       string
		m, days int32
		us      int64
		out     string
	}{
		{"P00021015e0", 34, 15, 0, "2 years 10 mons 15 days"},
		{"P00021015.0e0", 34, 15, 0, "2 years 10 mons 15 days"},
		{"PT001020e0", 0, 0, 620000000, "00:10:20"},
		{"PT001020.0e0", 0, 0, 620000000, "00:10:20"},
	} {
		v, err := d.Input(context.Background(), tc.s, -1)
		if err != nil {
			t.Fatal(tc.s, err)
		}
		m, days, us, err := v.Interval()
		if err != nil || m != tc.m || days != tc.days || us != tc.us {
			t.Fatal(tc.s, m, days, us, err)
		}
		out, err := d.Output(context.Background(), v)
		if err != nil || out != tc.out {
			t.Fatal(tc.s, out, err)
		}
	}
}

func TestIntervalPrecisionOverflowBranches(t *testing.T) {
	ctx := context.Background()
	d := testTypes()[Interval]
	u, _, err := d.ResolveModifiers(IntervalSecond, 2)
	if err != nil {
		t.Fatal(err)
	}
	const input = "2562047788:00:54.775807"
	v, err := d.Input(ctx, input, -1)
	if err != nil {
		t.Fatal(err)
	}
	m, days, us, err := v.Interval()
	if err != nil || m != 0 || days != 0 || us != math.MaxInt64 {
		t.Fatal(m, days, us, err)
	}
	_, err = d.Input(ctx, input, u.Typmod)
	intervalBoundaryError(t, err, "22008", "interval out of range")
	plan, err := PlanCoercion(d.Usage(), u, Assignment)
	if err != nil {
		t.Fatal(err)
	}
	_, err = plan.Apply(ctx, v, InputEnvironment{}, OutputEnvironment{})
	intervalBoundaryError(t, err, "22008", "interval out of range")
}

func TestIntervalOriginalInputBoundaries(t *testing.T) {
	d := testTypes()[Interval]
	for _, input := range []string{" PT1S", "PT1S ", " P1DT0x1p0S", "\tPT1S", "PT1S\n", "\u00a0PT1S", "\u00a01 second", "\u00a0infinity", "PT1S\u00a0", "1 second\u00a0", "infinity\u00a0"} {
		t.Run(input, func(t *testing.T) {
			_, err := d.Input(context.Background(), input, -1)
			intervalBoundaryError(t, err, "22007", "invalid input syntax for type interval: \""+input+"\"")
		})
	}
	for _, tc := range []struct {
		input        string
		months, days int32
		us           int64
	}{
		{" 1 second", 0, 0, 1000000}, {"1 second ", 0, 0, 1000000},
		{"\t1 second\n", 0, 0, 1000000}, {" \t1 second\r\n\f\v", 0, 0, 1000000},
		{" infinity", math.MaxInt32, math.MaxInt32, math.MaxInt64},
		{"infinity ", math.MaxInt32, math.MaxInt32, math.MaxInt64},
		{"\t-infinity\n", math.MinInt32, math.MinInt32, math.MinInt64},
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

func TestIntervalPrecisionOffsetOverflow(t *testing.T) {
	ctx := context.Background()
	d := testTypes()[Interval]
	u, _, err := d.ResolveModifiers(IntervalSecond, 5)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanCoercion(d.Usage(), u, Assignment)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		input string
		us    int64
	}{
		{"9223372036854775804 microseconds", 9223372036854775804},
		{"-9223372036854775804 microseconds", -9223372036854775804},
		{"9223372036854775803 microseconds", 9223372036854775803},
	} {
		t.Run(tc.input, func(t *testing.T) {
			v, err := d.Input(ctx, tc.input, -1)
			if err != nil {
				t.Fatal(err)
			}
			months, days, us, err := v.Interval()
			if err != nil || months != 0 || days != 0 || us != tc.us {
				t.Fatal(months, days, us, err)
			}
			_, err = d.Input(ctx, tc.input, u.Typmod)
			intervalBoundaryError(t, err, "22008", "interval out of range")
			_, err = plan.Apply(ctx, v, InputEnvironment{}, OutputEnvironment{})
			intervalBoundaryError(t, err, "22008", "interval out of range")
		})
	}
}

func TestIntervalPrecisionOffsetControls(t *testing.T) {
	ctx := context.Background()
	d := testTypes()[Interval]
	for _, tc := range []struct {
		input     string
		precision int32
		want      int64
	}{
		{"9223372036854775802 microseconds", 5, 9223372036854775800},
		{"-9223372036854775803 microseconds", 5, -9223372036854775800},
		{"9223372036854775800 microseconds", 5, 9223372036854775800},
		{"-9223372036854775800 microseconds", 5, -9223372036854775800},
		{"9223372036854775804 microseconds", 6, 9223372036854775804},
		{"-9223372036854775804 microseconds", 6, -9223372036854775804},
		{"4 microseconds", 5, 0}, {"-4 microseconds", 5, 0},
		{"5 microseconds", 5, 10}, {"-5 microseconds", 5, -10},
		{"14 microseconds", 5, 10}, {"-14 microseconds", 5, -10},
		{"15 microseconds", 5, 20}, {"-15 microseconds", 5, -20},
	} {
		t.Run(tc.input+"/"+string(rune('0'+tc.precision)), func(t *testing.T) {
			u, _, err := d.ResolveModifiers(IntervalSecond, tc.precision)
			if err != nil {
				t.Fatal(err)
			}
			input, err := d.Input(ctx, tc.input, -1)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := PlanCoercion(d.Usage(), u, Assignment)
			if err != nil {
				t.Fatal(err)
			}
			assigned, err := plan.Apply(ctx, input, InputEnvironment{}, OutputEnvironment{})
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := d.Input(ctx, tc.input, u.Typmod)
			if err != nil {
				t.Fatal(err)
			}
			for _, v := range []Value{parsed, assigned} {
				months, days, us, err := v.Interval()
				if err != nil || months != 0 || days != 0 || us != tc.want {
					t.Fatal(months, days, us, err)
				}
			}
		})
	}
	u, _, err := d.ResolveModifiers(IntervalDay, 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{"9223372036854775804 microseconds", "-9223372036854775804 microseconds"} {
		v, err := d.Input(ctx, input, u.Typmod)
		if err != nil {
			t.Fatal(err)
		}
		months, days, us, err := v.Interval()
		if err != nil || months != 0 || days != 0 || us != 0 {
			t.Fatal(months, days, us, err)
		}
	}
	u, _, err = d.ResolveModifiers(IntervalSecond, 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		input string
		sign  int
	}{{"infinity", 1}, {"-infinity", -1}} {
		v, err := d.Input(ctx, tc.input, u.Typmod)
		if err != nil || intervalInfinity(v) != tc.sign {
			t.Fatal(v, err)
		}
	}
}

func TestIntervalClockMagnitudeEndpoint(t *testing.T) {
	d := testTypes()[Interval]
	for _, tc := range []struct{ input, code, primary string }{
		{"-2562047788:00:54.775808", "22007", "invalid input syntax for type interval"},
		{"+2562047788:00:54.775808", "22007", "invalid input syntax for type interval"},
		{"2562047788:00:54.775808", "22015", "interval field value out of range"},
	} {
		if _, ok, over := intervalParseClock(tc.input, IntervalFullRange); ok || !over {
			t.Fatal(tc.input, ok, over)
		}
		for style := IntervalPostgres; style <= IntervalISO8601; style++ {
			_, err := d.InputWithEnvironment(context.Background(), tc.input, -1, InputEnvironment{IntervalStyle: style})
			intervalBoundaryError(t, err, tc.code, tc.primary+": \""+tc.input+"\"")
		}
	}
}

func TestIntervalClockEndpointControls(t *testing.T) {
	d := testTypes()[Interval]
	for _, tc := range []struct {
		input string
		us    int64
	}{
		{"-2562047788:00:54.775807", -math.MaxInt64},
		{"+2562047788:00:54.775807", math.MaxInt64},
		{"2562047788:00:54.775807", math.MaxInt64},
		{"-9223372036854775808 microseconds", math.MinInt64},
		{"+9223372036854775807 microseconds", math.MaxInt64},
	} {
		v, err := d.Input(context.Background(), tc.input, -1)
		if err != nil {
			t.Fatal(tc.input, err)
		}
		m, days, us, err := v.Interval()
		if err != nil || m != 0 || days != 0 || us != tc.us {
			t.Fatal(tc.input, m, days, us, err)
		}
	}
}

func TestIntervalMinimumTimeCanonical(t *testing.T) {
	ctx := context.Background()
	d := testTypes()[Interval]
	if literal, err := d.SQLLiteral(ctx, Null(d)); err != nil || literal != "NULL" {
		t.Fatal(literal, err)
	}
	for _, tc := range []struct {
		months, days int32
		us           int64
		literal      string
	}{
		{0, 0, math.MinInt64, "INTERVAL '+0 months +0 days -9223372036854775808 microseconds'"},
		{-14, 3, -14706000000, "INTERVAL '-14 months +3 days -14706000000 microseconds'"},
		{0, 0, math.MaxInt64, "INTERVAL '+0 months +0 days +9223372036854775807 microseconds'"},
		{0, 0, 0, "INTERVAL '+0 months +0 days +0 microseconds'"},
		{math.MinInt32, math.MaxInt32, math.MinInt64, "INTERVAL '-2147483648 months +2147483647 days -9223372036854775808 microseconds'"},
		{math.MaxInt32, math.MinInt32, math.MaxInt64, "INTERVAL '+2147483647 months -2147483648 days +9223372036854775807 microseconds'"},
		{math.MinInt32, math.MaxInt32, 0, "INTERVAL '-2147483648 months +2147483647 days +0 microseconds'"},
		{math.MinInt32, math.MinInt32, math.MinInt64, "INTERVAL '-infinity'"},
		{math.MaxInt32, math.MaxInt32, math.MaxInt64, "INTERVAL 'infinity'"},
	} {
		v, err := IntervalValue(d, tc.months, tc.days, tc.us)
		if err != nil {
			t.Fatal(err)
		}
		literal, err := d.SQLLiteral(ctx, v)
		if err != nil || literal != tc.literal {
			t.Fatal(literal, err, tc.literal)
		}
		input := literal[len("INTERVAL '") : len(literal)-1]
		for style := IntervalPostgres; style <= IntervalISO8601; style++ {
			got, err := d.InputWithEnvironment(ctx, input, -1, InputEnvironment{IntervalStyle: style})
			if err != nil || !SameDatum(got, v) {
				t.Fatal(style, got, err)
			}
		}
	}
}
