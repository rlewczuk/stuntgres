package sqltype

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"stuntgres/internal/sqlstate"
)

// Physical expectations are release-source constants, not exported SQL results.
func TestDatetimeArithmeticSignatures(t *testing.T) {
	types := testTypes()
	for _, table := range []struct {
		op         ArithmeticOperator
		signatures []ArithmeticSignature
	}{
		{ArithmeticAdd, []ArithmeticSignature{{Date, Int4, Date}, {Int4, Date, Date}, {Date, Time, Timestamp}, {Time, Date, Timestamp}, {Date, Timetz, Timestamptz}, {Timetz, Date, Timestamptz}, {Date, Interval, Timestamp}, {Interval, Date, Timestamp}, {Timestamp, Interval, Timestamp}, {Interval, Timestamp, Timestamp}, {Timestamptz, Interval, Timestamptz}, {Interval, Timestamptz, Timestamptz}, {Time, Interval, Time}, {Interval, Time, Time}, {Timetz, Interval, Timetz}, {Interval, Timetz, Timetz}, {Interval, Interval, Interval}}},
		{ArithmeticSubtract, []ArithmeticSignature{{Date, Int4, Date}, {Date, Date, Int4}, {Date, Interval, Timestamp}, {Timestamp, Interval, Timestamp}, {Timestamptz, Interval, Timestamptz}, {Timestamp, Timestamp, Interval}, {Timestamptz, Timestamptz, Interval}, {Time, Interval, Time}, {Timetz, Interval, Timetz}, {Time, Time, Interval}, {Interval, Interval, Interval}}},
		{ArithmeticMultiply, []ArithmeticSignature{{Interval, Float8, Interval}, {Float8, Interval, Interval}}},
		{ArithmeticDivide, []ArithmeticSignature{{Interval, Float8, Interval}}},
	} {
		for _, s := range table.signatures {
			got, err := ResolveArithmetic(table.op, types[s.Left], types[s.Right], false)
			if err != nil || got != s {
				t.Fatal(table.op, s, got, err)
			}
			p, err := BindArithmetic(table.op, types[s.Left], types[s.Right], types[s.Result])
			if err != nil || p.ResultType() != types[s.Result] {
				t.Fatal(s, p, err)
			}
			stable := table.op == ArithmeticAdd && (s.Left == Timestamptz && s.Right == Interval || s.Left == Interval && s.Right == Timestamptz) || table.op == ArithmeticSubtract && s.Left == Timestamptz && s.Right == Interval
			if p.SessionDependent() != stable {
				t.Fatal("intrinsic volatility", s, p)
			}
			if _, err := BindArithmetic(table.op, types[s.Left], types[s.Right], types[Bool]); err == nil {
				t.Fatal("wrong result admitted", s)
			}
			v, err := p.Binary(context.Background(), Null(types[s.Left]), Null(types[s.Right]), InputEnvironment{})
			if err != nil || !v.IsNull() || v.Type() != types[s.Result] {
				t.Fatal("strict result", s, v, err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := p.Binary(ctx, Null(types[s.Left]), Null(types[s.Right]), InputEnvironment{}); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		}
	}
	for _, tc := range []struct {
		op   ArithmeticOperator
		l, r ID
	}{{ArithmeticAdd, Date, Date}, {ArithmeticSubtract, Timetz, Timetz}, {ArithmeticDivide, Float8, Interval}, {ArithmeticSubtract, Interval, Date}} {
		if _, err := BindArithmetic(tc.op, types[tc.l], types[tc.r], types[Interval]); err == nil {
			t.Fatal("unreleased provider", tc)
		}
	}
	if _, err := BindArithmetic(ArithmeticAdd, types[Interval], nil, types[Interval]); err == nil {
		t.Fatal("unary plus interval")
	}
	for _, tc := range []struct {
		op   ArithmeticOperator
		l, r ID
		want ArithmeticSignature
		code string
	}{
		{ArithmeticAdd, Date, 0, ArithmeticSignature{}, "42725"},
		{ArithmeticSubtract, Date, 0, ArithmeticSignature{Date, Date, Int4}, ""},
		{ArithmeticAdd, Interval, 0, ArithmeticSignature{Interval, Interval, Interval}, ""},
		{ArithmeticMultiply, Interval, 0, ArithmeticSignature{Interval, Float8, Interval}, ""},
		{ArithmeticMultiply, 0, 0, ArithmeticSignature{}, "42725"},
	} {
		got, err := ResolveArithmetic(tc.op, types[tc.l], types[tc.r], false)
		if tc.code == "" {
			if err != nil || got != tc.want {
				t.Fatal(tc, got, err)
			}
		} else {
			datetimeArithmeticDiagnostic(t, err, tc.code, "ambiguous arithmetic operands")
		}
	}
	if equalityCategory(Interval) != 'T' || !equalityPreferred(Interval) {
		t.Fatal("interval category/preference")
	}
}

func datetimeArithmeticDiagnostic(t *testing.T, err error, code, message string) {
	t.Helper()
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != code || d.Message != message || d.Position != "" || d.Detail != "" || d.Hint != "" || d.Context != "" {
		t.Fatalf("diagnostic %#v want %s %s", err, code, message)
	}
}

func TestDatetimeArithmeticPhysicalComponents(t *testing.T) {
	d := testTypes()
	ctx := context.Background()
	for _, tc := range []struct {
		op   ArithmeticOperator
		a, b intervalAvgComponents
		want intervalAvgComponents
	}{
		{ArithmeticAdd, intervalAvgComponents{1, 0, 0}, intervalAvgComponents{0, 30, 0}, intervalAvgComponents{1, 30, 0}},
		{ArithmeticSubtract, intervalAvgComponents{0, 1, 0}, intervalAvgComponents{0, 0, 3600000000}, intervalAvgComponents{0, 1, -3600000000}},
		{ArithmeticAdd, intervalAvgComponents{0, 0, 86400000000}, intervalAvgComponents{0, 0, 3600000000}, intervalAvgComponents{0, 0, 90000000000}},
	} {
		p, _ := BindArithmetic(tc.op, d[Interval], d[Interval], d[Interval])
		a, _ := IntervalValue(d[Interval], tc.a.months, tc.a.days, tc.a.micros)
		b, _ := IntervalValue(d[Interval], tc.b.months, tc.b.days, tc.b.micros)
		v, err := p.Binary(ctx, a, b, InputEnvironment{})
		m, day, u, _ := v.Interval()
		if err != nil || (intervalAvgComponents{m, day, u}) != tc.want {
			t.Fatal(tc, v, err)
		}
	}
	for _, tc := range []struct {
		op     ArithmeticOperator
		a      intervalAvgComponents
		factor float64
		want   intervalAvgComponents
	}{
		{ArithmeticMultiply, intervalAvgComponents{1, 0, 0}, 0.5, intervalAvgComponents{0, 15, 0}},
		{ArithmeticDivide, intervalAvgComponents{1, 0, 0}, 4, intervalAvgComponents{0, 7, 43200000000}},
		{ArithmeticDivide, intervalAvgComponents{1, 0, 0}, 7, intervalAvgComponents{0, 4, 24685689600}},
		{ArithmeticMultiply, intervalAvgComponents{0, 1, 0}, -1.5, intervalAvgComponents{0, -1, -43200000000}},
		{ArithmeticMultiply, intervalAvgComponents{0, 0, 3}, 0.5, intervalAvgComponents{0, 0, 2}},
		{ArithmeticMultiply, intervalAvgComponents{0, 0, 1}, 0.5, intervalAvgComponents{}},
		{ArithmeticMultiply, intervalAvgComponents{0, 0, 86400000000}, 2, intervalAvgComponents{0, 0, 172800000000}},
		{ArithmeticDivide, intervalAvgComponents{1, 2, 3}, math.Inf(1), intervalAvgComponents{}},
	} {
		p, _ := BindArithmetic(tc.op, d[Interval], d[Float8], d[Interval])
		a, _ := IntervalValue(d[Interval], tc.a.months, tc.a.days, tc.a.micros)
		b, _ := Float(d[Float8], tc.factor)
		v, err := p.Binary(ctx, a, b, InputEnvironment{})
		m, day, u, _ := v.Interval()
		if err != nil || (intervalAvgComponents{m, day, u}) != tc.want {
			t.Fatal(tc, v, err)
		}
		if tc.op == ArithmeticMultiply {
			q, _ := BindArithmetic(tc.op, d[Float8], d[Interval], d[Interval])
			w, err := q.Binary(ctx, b, a, InputEnvironment{})
			if err != nil || !SameDatum(v, w) {
				t.Fatal("commutation", w, err)
			}
		}
	}
	p, _ := BindArithmetic(ArithmeticSubtract, d[Interval], nil, d[Interval])
	for _, c := range []intervalAvgComponents{{math.MinInt32, 0, 0}, {0, math.MinInt32, 0}, {0, 0, math.MinInt64}} {
		v, _ := IntervalValue(d[Interval], c.months, c.days, c.micros)
		_, err := p.Unary(ctx, v, InputEnvironment{})
		datetimeArithmeticDiagnostic(t, err, "22008", "interval out of range")
	}
	for _, tc := range []struct {
		op            ArithmeticOperator
		factor        float64
		code, message string
	}{{ArithmeticDivide, 0, "22012", "division by zero"}, {ArithmeticMultiply, math.NaN(), "22008", "interval out of range"}, {ArithmeticMultiply, math.Inf(1), "22008", "interval out of range"}} {
		p, _ := BindArithmetic(tc.op, d[Interval], d[Float8], d[Interval])
		a, _ := IntervalValue(d[Interval], 1, -30, 0)
		b, _ := Float(d[Float8], tc.factor)
		_, err := p.Binary(ctx, a, b, InputEnvironment{})
		datetimeArithmeticDiagnostic(t, err, tc.code, tc.message)
	}
}

func TestDatetimeArithmeticCalendarAndClock(t *testing.T) {
	d := testTypes()
	ctx := context.Background()
	for _, tc := range []struct {
		y, m, day    int
		months, days int32
		micros       int64
		wy, wm, wd   int
	}{{2024, 1, 31, 1, 0, 0, 2024, 2, 29}, {2023, 1, 31, 1, 0, 0, 2023, 2, 28}, {2024, 3, 31, 2, 0, 0, 2024, 5, 31}, {2024, 3, 31, -1, 0, 0, 2024, 2, 29}, {0, 12, 31, 0, 1, 0, 1, 1, 1}} {
		n, _ := civilTimestamp(tc.y, tc.m, tc.day, 12, 34, 56, 123456)
		want, _ := civilTimestamp(tc.wy, tc.wm, tc.wd, 12, 34, 56, 123456)
		a, _ := TimestampValue(d[Timestamp], n)
		b, _ := IntervalValue(d[Interval], tc.months, tc.days, tc.micros)
		p, _ := BindArithmetic(ArithmeticAdd, d[Timestamp], d[Interval], d[Timestamp])
		v, err := p.Binary(ctx, a, b, InputEnvironment{})
		if err != nil || v.integer != want {
			t.Fatal(tc, v, err)
		}
		if allocations := testing.AllocsPerRun(10, func() { _, _ = p.Binary(ctx, a, b, InputEnvironment{}) }); allocations != 0 {
			t.Fatal("finite calendar allocations", allocations)
		}
	}
	p, _ := BindArithmetic(ArithmeticSubtract, d[Date], d[Date], d[Int4])
	a, _ := DateValue(d[Date], dateMaximum)
	b, _ := DateValue(d[Date], dateMinimum)
	v, err := p.Binary(ctx, a, b, InputEnvironment{})
	if err != nil || v.integer != 2147483493 {
		t.Fatal(v, err)
	}
	for _, id := range []ID{Time, Timetz} {
		p, _ := BindArithmetic(ArithmeticAdd, d[id], d[Interval], d[id])
		a := Value{typ: d[id], integer: microsPerDay, west: -7384}
		b, _ := IntervalValue(d[Interval], 2, 3, 0)
		v, err := p.Binary(ctx, a, b, InputEnvironment{})
		if err != nil || v.integer != 0 || id == Timetz && v.west != -7384 {
			t.Fatal(v, err)
		}
		b, _ = IntervalValue(d[Interval], 0, 0, math.MaxInt64)
		v, err = p.Binary(ctx, a, b, InputEnvironment{})
		if err != nil || v.integer != 71945224191 {
			t.Fatal("released wrapped clock", v, err)
		}
	}
	p, _ = BindArithmetic(ArithmeticSubtract, d[Time], d[Time], d[Interval])
	a, _ = TimeValue(d[Time], microsPerDay)
	b, _ = TimeValue(d[Time], 0)
	v, err = p.Binary(ctx, a, b, InputEnvironment{})
	if err != nil || v.days != 0 || v.integer != microsPerDay {
		t.Fatal("clock subtraction must not justify", v, err)
	}
	p, _ = BindArithmetic(ArithmeticAdd, d[Timestamp], d[Interval], d[Timestamp])
	a, _ = TimestampValue(d[Timestamp], timestampEnd-1)
	b, _ = IntervalValue(d[Interval], 0, 0, 1)
	_, err = p.Binary(ctx, a, b, InputEnvironment{})
	datetimeArithmeticDiagnostic(t, err, "22008", "timestamp out of range")
}

func TestDatetimeArithmeticEmbeddedDST(t *testing.T) {
	d := testTypes()
	zone, err := ResolveZone("America/Denver")
	if err != nil {
		t.Fatal(err)
	}
	// 2005-04-02 12:00-07:00 is 19:00 UTC; calendar day and elapsed day differ.
	n, _ := civilTimestamp(2005, 4, 2, 19, 0, 0, 0)
	a, _ := TimestampValue(d[Timestamptz], n)
	p, _ := BindArithmetic(ArithmeticAdd, d[Timestamptz], d[Interval], d[Timestamptz])
	for _, tc := range []struct {
		day    int32
		micros int64
		hour   int
	}{{1, 0, 18}, {0, 86400000000, 19}} {
		b, _ := IntervalValue(d[Interval], 0, tc.day, tc.micros)
		want, _ := civilTimestamp(2005, 4, 3, tc.hour, 0, 0, 0)
		v, err := p.Binary(context.Background(), a, b, InputEnvironment{Zone: zone, TransactionStart: time.Date(2005, 4, 2, 0, 0, 0, 0, time.UTC)})
		if err != nil || v.integer != want {
			t.Fatal(tc, v, err)
		}
	}
}

func TestDatetimeArithmeticInfinityAndCheckedBounds(t *testing.T) {
	d := testTypes()
	ctx := context.Background()
	positive, _ := IntervalValue(d[Interval], math.MaxInt32, math.MaxInt32, math.MaxInt64)
	negative, _ := IntervalValue(d[Interval], math.MinInt32, math.MinInt32, math.MinInt64)
	zero, _ := IntervalValue(d[Interval], 0, 0, 0)
	for _, tc := range []struct {
		op      ArithmeticOperator
		a, b    Value
		result  ID
		want    Value
		message string
	}{
		{ArithmeticAdd, positive, negative, Interval, Value{}, "interval out of range"},
		{ArithmeticSubtract, positive, positive, Interval, Value{}, "interval out of range"},
		{ArithmeticAdd, positive, zero, Interval, positive, ""},
		{ArithmeticSubtract, negative, positive, Interval, negative, ""},
		{ArithmeticAdd, Value{typ: d[Timestamp], integer: TimestampPositiveInfinity}, negative, Timestamp, Value{}, "timestamp out of range"},
		{ArithmeticSubtract, Value{typ: d[Timestamp], integer: TimestampNegativeInfinity}, Value{typ: d[Timestamp], integer: TimestampNegativeInfinity}, Interval, Value{}, "interval out of range"},
		{ArithmeticSubtract, Value{typ: d[Date], integer: DatePositiveInfinity}, Value{typ: d[Date], integer: 0}, Int4, Value{}, "cannot subtract infinite dates"},
		{ArithmeticAdd, Value{typ: d[Time], integer: 0}, positive, Time, Value{}, "cannot add infinite interval to time"},
		{ArithmeticSubtract, Value{typ: d[Timetz], integer: 0}, negative, Timetz, Value{}, "cannot subtract infinite interval from time"},
		{ArithmeticAdd, Value{typ: d[Timestamp], integer: 1}, Value{typ: d[Interval], integer: math.MaxInt64 - 1}, Timestamp, Value{}, "timestamp out of range"},
		{ArithmeticAdd, Value{typ: d[Interval], months: math.MaxInt32}, Value{typ: d[Interval], months: 1}, Interval, Value{}, "interval out of range"},
		{ArithmeticSubtract, Value{typ: d[Interval], integer: math.MinInt64}, Value{typ: d[Interval], integer: math.MinInt64}, Interval, zero, ""},
		{ArithmeticSubtract, Value{typ: d[Timestamp], integer: timestampMinimum}, Value{typ: d[Interval], days: 1}, Timestamp, Value{}, "timestamp out of range"},
		{ArithmeticSubtract, Value{typ: d[Date], integer: dateMinimum}, Value{typ: d[Int4], integer: 1}, Date, Value{}, "date out of range"},
	} {
		p, err := BindArithmetic(tc.op, tc.a.Type(), tc.b.Type(), d[tc.result])
		if err != nil {
			t.Fatal(err)
		}
		v, err := p.Binary(ctx, tc.a, tc.b, InputEnvironment{})
		if tc.message != "" {
			datetimeArithmeticDiagnostic(t, err, "22008", tc.message)
		} else if err != nil || !SameDatum(v, tc.want) {
			t.Fatal(tc, v, err)
		}
	}
	p, _ := BindArithmetic(ArithmeticSubtract, d[Interval], nil, d[Interval])
	v, err := p.Unary(ctx, positive, InputEnvironment{})
	if err != nil || !SameDatum(v, negative) {
		t.Fatal(v, err)
	}
	finite, _ := IntervalValue(d[Interval], -math.MaxInt32, -math.MaxInt32, -math.MaxInt64)
	_, err = p.Unary(ctx, finite, InputEnvironment{})
	datetimeArithmeticDiagnostic(t, err, "22008", "interval out of range")
	// Subtract must negate before even an infinite timestamp can return.
	p, _ = BindArithmetic(ArithmeticSubtract, d[Timestamp], d[Interval], d[Timestamp])
	_, err = p.Binary(ctx, Value{typ: d[Timestamp], integer: TimestampPositiveInfinity}, Value{typ: d[Interval], months: math.MinInt32}, InputEnvironment{})
	datetimeArithmeticDiagnostic(t, err, "22008", "interval out of range")
}

func TestDatetimeArithmeticIntervalOutputCoercion(t *testing.T) {
	types := testTypes()
	plan, err := PlanCoercion(types[Interval].Usage(), types[Text].Usage(), Explicit)
	if err != nil || !plan.SessionDependent() || plan.method != viaIO {
		t.Fatal(plan, err)
	}
	v, _ := IntervalValue(types[Interval], 0, 1, 3600000000)
	for _, tc := range []struct {
		style IntervalStyle
		want  string
	}{
		{IntervalPostgres, "1 day 01:00:00"},
		{IntervalPostgresVerbose, "@ 1 day 1 hour"},
		{IntervalSQLStandard, "1 1:00:00"},
		{IntervalISO8601, "P1DT1H"},
	} {
		result, err := plan.Apply(context.Background(), v, InputEnvironment{}, OutputEnvironment{IntervalStyle: tc.style})
		if err != nil {
			t.Fatal(err)
		}
		text, err := result.Text()
		if err != nil || text != tc.want {
			t.Fatal(tc, text, err)
		}
	}
	result, err := plan.Apply(context.Background(), Null(types[Interval]), InputEnvironment{}, OutputEnvironment{})
	if err != nil || !result.IsNull() || result.Type() != types[Text] {
		t.Fatal(result, err)
	}
}

func TestDatetimeArithmeticZonedJulianBoundary(t *testing.T) {
	d := testTypes()
	zone, err := ResolveZone("Etc/GMT+1")
	if err != nil {
		t.Fatal(err)
	}
	env := InputEnvironment{Zone: zone}
	interval, _ := IntervalValue(d[Interval], 0, 1, 0)
	negative, _ := IntervalValue(d[Interval], 0, -1, 0)
	// Independent physical instants from the release timestamp epoch/range.
	// Local Julian -1 is legal here; the actual destination instant is finite.
	for _, tc := range []struct {
		op       ArithmeticOperator
		commuted bool
	}{{ArithmeticSubtract, false}, {ArithmeticAdd, false}, {ArithmeticAdd, true}} {
		a, _ := TimestampValue(d[Timestamptz], -211813399800000000)
		b := interval
		if tc.op == ArithmeticAdd {
			b = negative
		}
		left, right := a, b
		if tc.commuted {
			left, right = b, a
		}
		p, err := BindArithmetic(tc.op, left.Type(), right.Type(), d[Timestamptz])
		if err != nil {
			t.Fatal(err)
		}
		result, err := p.Binary(context.Background(), left, right, env)
		if err != nil || result.integer != -211813486200000000 {
			t.Fatal(tc, result, err)
		}
		a, _ = TimestampValue(d[Timestamptz], -211813401600000000)
		left, right = a, b
		if tc.commuted {
			left, right = b, a
		}
		result, err = p.Binary(context.Background(), left, right, env)
		if err != nil || result.integer != -211813488000000000 {
			t.Fatal("exact minimum", tc, result, err)
		}
		// The neighboring destination instant is minimum-1 microsecond.
		a, _ = TimestampValue(d[Timestamptz], -211813401600000001)
		left, right = a, b
		if tc.commuted {
			left, right = b, a
		}
		_, err = p.Binary(context.Background(), left, right, env)
		datetimeArithmeticDiagnostic(t, err, "22008", "timestamp out of range")
	}
}

func TestDatetimeArithmeticZonedMonthStages(t *testing.T) {
	d := testTypes()
	zone, err := ResolveZone("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	env := InputEnvironment{Zone: zone}
	// UTC fields are independent goldens, not calculated from provider output.
	for _, tc := range []struct {
		month, day, hour, minute                 int
		months, days                             int32
		wantMonth, wantDay, wantHour, wantMinute int
	}{
		{2, 11, 7, 30, 1, 0, 3, 11, 7, 30},
		{10, 4, 5, 30, 1, 0, 11, 4, 6, 30},
		{2, 11, 7, 30, 1, 1, 3, 12, 7, 30},
		{10, 4, 5, 30, 1, 1, 11, 5, 6, 30},
		{4, 11, 6, 30, -1, 0, 3, 11, 7, 30},
		{12, 4, 6, 30, -1, 0, 11, 4, 6, 30},
		{4, 11, 6, 30, -1, -1, 3, 10, 8, 30},
		{12, 4, 6, 30, -1, -1, 11, 3, 5, 30},
		{4, 11, 6, 30, -1, 1, 3, 12, 7, 30},
	} {
		start, _ := civilTimestamp(2018, tc.month, tc.day, tc.hour, tc.minute, 0, 0)
		want, _ := civilTimestamp(2018, tc.wantMonth, tc.wantDay, tc.wantHour, tc.wantMinute, 0, 0)
		a, _ := TimestampValue(d[Timestamptz], start)
		for _, route := range []struct {
			op       ArithmeticOperator
			commuted bool
		}{{ArithmeticAdd, false}, {ArithmeticAdd, true}, {ArithmeticSubtract, false}} {
			months, days := tc.months, tc.days
			if route.op == ArithmeticSubtract {
				months, days = -months, -days
			}
			b, _ := IntervalValue(d[Interval], months, days, 0)
			left, right := a, b
			if route.commuted {
				left, right = b, a
			}
			p, err := BindArithmetic(route.op, left.Type(), right.Type(), d[Timestamptz])
			if err != nil {
				t.Fatal(err)
			}
			result, err := p.Binary(context.Background(), left, right, env)
			if err != nil || result.integer != want {
				t.Fatal(tc, route, result, err, "want", want)
			}
		}
	}
}
