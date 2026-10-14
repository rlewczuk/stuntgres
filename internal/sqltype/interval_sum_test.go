package sqltype

import (
	"errors"
	"math"
	"testing"
)

func TestIntervalAggregateSumResult(t *testing.T) {
	d := testTypes()[Interval]
	for _, c := range []struct {
		name   string
		inputs []intervalAvgComponents
		want   intervalAvgComponents
	}{
		{"calendar", []intervalAvgComponents{{1, 0, 0}, {0, 30, 0}}, intervalAvgComponents{1, 30, 0}},
		{"day_time", []intervalAvgComponents{{0, 1, 0}, {0, 0, 86400000000}}, intervalAvgComponents{0, 1, 86400000000}},
		{"exact_microseconds", []intervalAvgComponents{{0, 0, 9007199254740993}}, intervalAvgComponents{0, 0, 9007199254740993}},
		{"time_max", []intervalAvgComponents{{0, 0, math.MaxInt64}}, intervalAvgComponents{0, 0, math.MaxInt64}},
		{"time_min", []intervalAvgComponents{{0, 0, math.MinInt64}}, intervalAvgComponents{0, 0, math.MinInt64}},
		{"month_max", []intervalAvgComponents{{math.MaxInt32, 0, 0}}, intervalAvgComponents{math.MaxInt32, 0, 0}},
		{"month_min", []intervalAvgComponents{{math.MinInt32, 0, 0}}, intervalAvgComponents{math.MinInt32, 0, 0}},
		{"day_max", []intervalAvgComponents{{0, math.MaxInt32, 0}}, intervalAvgComponents{0, math.MaxInt32, 0}},
		{"day_min", []intervalAvgComponents{{0, math.MinInt32, 0}}, intervalAvgComponents{0, math.MinInt32, 0}},
		{"cancellation", []intervalAvgComponents{{1, 2, 3}, {-1, -2, -3}}, intervalAvgComponents{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var state IntervalAvgState
			for _, in := range c.inputs {
				v, err := IntervalValue(d, in.months, in.days, in.micros)
				if err != nil {
					t.Fatal(err)
				}
				if err := state.Add(v); err != nil {
					t.Fatal(err)
				}
			}
			before := state
			for range 2 {
				out, err := state.SumResult(d)
				if err != nil || out.IsNull() || out.Type() != d {
					t.Fatal(out, err)
				}
				m, days, u, err := out.Interval()
				if err != nil || (intervalAvgComponents{m, days, u}) != c.want {
					t.Fatalf("components %d/%d/%d %v want %+v", m, days, u, err, c.want)
				}
			}
			if state != before {
				t.Fatal("SumResult consumed state")
			}
		})
	}
	var empty IntervalAvgState
	if err := empty.Add(Null(d)); err != nil {
		t.Fatal(err)
	}
	out, err := empty.SumResult(d)
	if err != nil || !out.IsNull() || out.Type() != d {
		t.Fatal(out, err)
	}
	for _, bad := range []*Descriptor{nil, testTypes()[Int4]} {
		_, err := empty.SumResult(bad)
		var diagnostic interface{ SQLState() string }
		if err == nil || errors.As(err, &diagnostic) {
			t.Fatal("descriptor misuse must be ordinary", err)
		}
	}
	if _, err := (*IntervalAvgState)(nil).SumResult(d); err == nil {
		t.Fatal("nil state accepted")
	}
}

func TestIntervalAggregateSumResultBoundaries(t *testing.T) {
	d := testTypes()[Interval]
	for _, c := range []struct{ first, second intervalAvgComponents }{
		{intervalAvgComponents{math.MaxInt32, 0, 0}, intervalAvgComponents{1, 0, 0}},
		{intervalAvgComponents{math.MinInt32, 0, 0}, intervalAvgComponents{-1, 0, 0}},
		{intervalAvgComponents{0, math.MaxInt32, 0}, intervalAvgComponents{0, 1, 0}},
		{intervalAvgComponents{0, math.MinInt32, 0}, intervalAvgComponents{0, -1, 0}},
		{intervalAvgComponents{0, 0, math.MaxInt64}, intervalAvgComponents{0, 0, 1}},
		{intervalAvgComponents{0, 0, math.MinInt64}, intervalAvgComponents{0, 0, -1}},
		{intervalAvgComponents{math.MaxInt32, math.MaxInt32, 0}, intervalAvgComponents{0, 0, math.MaxInt64}},
		{intervalAvgComponents{math.MinInt32, math.MinInt32, 0}, intervalAvgComponents{0, 0, math.MinInt64}},
	} {
		var state IntervalAvgState
		v, _ := IntervalValue(d, c.first.months, c.first.days, c.first.micros)
		if err := state.Add(v); err != nil {
			t.Fatal(err)
		}
		before := state
		v, _ = IntervalValue(d, c.second.months, c.second.days, c.second.micros)
		intervalAvgAssertRange(t, state.Add(v))
		if state != before {
			t.Fatal("failed transition changed state")
		}
		out, err := state.SumResult(d)
		if err != nil {
			t.Fatal(err)
		}
		m, days, u, err := out.Interval()
		if err != nil || (intervalAvgComponents{m, days, u}) != c.first {
			t.Fatal("failed transition changed result", m, days, u, err)
		}
	}
	positive, _ := IntervalValue(d, math.MaxInt32, math.MaxInt32, math.MaxInt64)
	negative, _ := IntervalValue(d, math.MinInt32, math.MinInt32, math.MinInt64)
	finite, _ := IntervalValue(d, 0, 2, 3)
	for _, infinity := range []Value{positive, negative} {
		var state IntervalAvgState
		for _, v := range []Value{infinity, finite, Null(d), infinity} {
			if err := state.Add(v); err != nil {
				t.Fatal(err)
			}
		}
		before := state
		for range 2 {
			out, err := state.SumResult(d)
			if err != nil || intervalInfinity(out) != intervalInfinity(infinity) {
				t.Fatal(out, err)
			}
		}
		if state != before {
			t.Fatal("infinity finalizer changed state")
		}
	}
	var mixed IntervalAvgState
	mixed.Add(positive)
	mixed.Add(negative)
	before := mixed
	for range 2 {
		_, err := mixed.SumResult(d)
		intervalAvgAssertRange(t, err)
	}
	if mixed != before {
		t.Fatal("failed finalization changed state")
	}
	var afterInfinity IntervalAvgState
	afterInfinity.Add(positive)
	max, _ := IntervalValue(d, 0, math.MaxInt32, 0)
	one, _ := IntervalValue(d, 0, 1, 0)
	afterInfinity.Add(max)
	before = afterInfinity
	intervalAvgAssertRange(t, afterInfinity.Add(one))
	if afterInfinity != before {
		t.Fatal("overflow after infinity changed state")
	}
	out, err := afterInfinity.SumResult(d)
	if err != nil || intervalInfinity(out) != 1 {
		t.Fatal(out, err)
	}
}
