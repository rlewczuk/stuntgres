package sqltype

import (
	"errors"
	"math"
	"stuntgres/internal/sqlstate"
	"testing"
)

type intervalAvgComponents struct {
	months, days int32
	micros       int64
}

func TestIntervalAvgArithmetic(t *testing.T) {
	d := testTypes()[Interval]
	for _, tc := range []struct {
		name   string
		inputs []intervalAvgComponents
		want   intervalAvgComponents
	}{
		{"month", []intervalAvgComponents{{1, 0, 0}, {}}, intervalAvgComponents{0, 15, 0}},
		{"day", []intervalAvgComponents{{0, 1, 0}, {}}, intervalAvgComponents{0, 0, 43200000000}},
		{"physical_month", []intervalAvgComponents{{1, 0, 0}, {0, 30, 0}}, intervalAvgComponents{0, 30, 0}},
		{"physical_time", []intervalAvgComponents{{0, 1, 0}, {0, 0, 86400000000}}, intervalAvgComponents{0, 0, 86400000000}},
		{"negative_month", []intervalAvgComponents{{-1, 0, 0}, {}}, intervalAvgComponents{0, -15, 0}},
		{"negative_day", []intervalAvgComponents{{0, -1, 0}, {}}, intervalAvgComponents{0, 0, -43200000000}},
		{"mixed", []intervalAvgComponents{{1, -1, 0}, {}}, intervalAvgComponents{0, 15, -43200000000}},
		{"tsround", []intervalAvgComponents{{1, 0, 0}, {}, {}, {}, {}, {}, {}}, intervalAvgComponents{0, 4, 24685689600}},
		{"one_tie", []intervalAvgComponents{{0, 0, 1}, {}}, intervalAvgComponents{}},
		{"three_tie", []intervalAvgComponents{{0, 0, 3}, {}}, intervalAvgComponents{0, 0, 2}},
		{"five_tie", []intervalAvgComponents{{0, 0, 5}, {}}, intervalAvgComponents{0, 0, 2}},
		{"negative_one_tie", []intervalAvgComponents{{0, 0, -1}, {}}, intervalAvgComponents{}},
		{"negative_three_tie", []intervalAvgComponents{{0, 0, -3}, {}}, intervalAvgComponents{0, 0, -2}},
		{"negative_five_tie", []intervalAvgComponents{{0, 0, -5}, {}}, intervalAvgComponents{0, 0, -2}},
		{"float_precision", []intervalAvgComponents{{0, 0, 9007199254740993}}, intervalAvgComponents{0, 0, 9007199254740992}},
		{"time_min", []intervalAvgComponents{{0, 0, math.MinInt64}}, intervalAvgComponents{0, 0, math.MinInt64}},
		{"month_max", []intervalAvgComponents{{math.MaxInt32, 0, 0}}, intervalAvgComponents{math.MaxInt32, 0, 0}},
		{"month_min", []intervalAvgComponents{{math.MinInt32, 0, 0}}, intervalAvgComponents{math.MinInt32, 0, 0}},
		{"day_max", []intervalAvgComponents{{0, math.MaxInt32, 0}}, intervalAvgComponents{0, math.MaxInt32, 0}},
		{"day_min", []intervalAvgComponents{{0, math.MinInt32, 0}}, intervalAvgComponents{0, math.MinInt32, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var s IntervalAvgState
			for _, c := range tc.inputs {
				v, err := IntervalValue(d, c.months, c.days, c.micros)
				if err != nil {
					t.Fatal(err)
				}
				if err := s.Add(v); err != nil {
					t.Fatal(err)
				}
			}
			before := s
			for range 2 {
				v, err := s.Result(d)
				if err != nil {
					t.Fatal(err)
				}
				m, day, u, err := v.Interval()
				if err != nil || v.Type() != d || (intervalAvgComponents{m, day, u}) != tc.want {
					t.Fatalf("result %d,%d,%d/%v want %+v", m, day, u, err, tc.want)
				}
			}
			if s != before {
				t.Fatal("Result mutated state")
			}
		})
	}
	for _, sign := range []int32{1, -1} {
		var s IntervalAvgState
		v, _ := IntervalValue(d, sign, sign, 0)
		if err := s.Add(v); err != nil {
			t.Fatal(err)
		}
		zero, _ := IntervalValue(d, 0, 0, 0)
		for range 30 {
			if err := s.Add(zero); err != nil {
				t.Fatal(err)
			}
		}
		result, err := s.Result(d)
		if err != nil {
			t.Fatal(err)
		}
		m, days, u, err := result.Interval()
		if err != nil || m != 0 || days != sign || u != int64(sign)*5574 {
			t.Fatal("TSROUND day carry", m, days, u, err)
		}
	}
}

func intervalAvgAssertRange(t *testing.T, err error) {
	t.Helper()
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != "22008" || d.Message != "interval out of range" || d.Position != "" || d.Hint != "" || d.Detail != "" || d.Context != "" {
		t.Fatalf("range diagnostic: %#v", err)
	}
}

func TestIntervalAvgBoundariesAndNondestructiveFailure(t *testing.T) {
	d := testTypes()[Interval]
	for _, tc := range []struct {
		name          string
		first, second intervalAvgComponents
	}{
		{"month_positive", intervalAvgComponents{math.MaxInt32, 0, 0}, intervalAvgComponents{1, 0, 0}},
		{"month_negative", intervalAvgComponents{math.MinInt32, 0, 0}, intervalAvgComponents{-1, 0, 0}},
		{"day_positive", intervalAvgComponents{0, math.MaxInt32, 0}, intervalAvgComponents{0, 1, 0}},
		{"day_negative", intervalAvgComponents{0, math.MinInt32, 0}, intervalAvgComponents{0, -1, 0}},
		{"time_positive", intervalAvgComponents{0, 0, math.MaxInt64}, intervalAvgComponents{0, 0, 1}},
		{"time_negative", intervalAvgComponents{0, 0, math.MinInt64}, intervalAvgComponents{0, 0, -1}},
		{"reserved_positive", intervalAvgComponents{math.MaxInt32, math.MaxInt32, 0}, intervalAvgComponents{0, 0, math.MaxInt64}},
		{"reserved_negative", intervalAvgComponents{math.MinInt32, math.MinInt32, 0}, intervalAvgComponents{0, 0, math.MinInt64}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var s IntervalAvgState
			v, _ := IntervalValue(d, tc.first.months, tc.first.days, tc.first.micros)
			if err := s.Add(v); err != nil {
				t.Fatal(err)
			}
			before := s
			v, _ = IntervalValue(d, tc.second.months, tc.second.days, tc.second.micros)
			intervalAvgAssertRange(t, s.Add(v))
			if s != before {
				t.Fatal("failed Add changed state")
			}
		})
	}
	var s IntervalAvgState
	max, _ := IntervalValue(d, 0, 0, math.MaxInt64)
	if err := s.Add(max); err != nil {
		t.Fatal(err)
	}
	before := s
	_, err := s.Result(d)
	intervalAvgAssertRange(t, err)
	if s != before {
		t.Fatal("failed Result changed state")
	}
}

func TestIntervalAvgEmptyInfinityAndOwnership(t *testing.T) {
	types := testTypes()
	d := types[Interval]
	var empty IntervalAvgState
	v, err := empty.Result(d)
	if err != nil || !v.IsNull() || v.Type() != d {
		t.Fatal(v, err)
	}
	if err := empty.Add(Null(d)); err != nil {
		t.Fatal(err)
	}
	if empty.count != 0 {
		t.Fatal("NULL counted")
	}
	finite, _ := IntervalValue(d, 0, 2, 0)
	positive, _ := IntervalValue(d, math.MaxInt32, math.MaxInt32, math.MaxInt64)
	negative, _ := IntervalValue(d, math.MinInt32, math.MinInt32, math.MinInt64)
	for _, infinity := range []Value{positive, negative} {
		var s IntervalAvgState
		for _, value := range []Value{infinity, finite, Null(d), infinity} {
			if err := s.Add(value); err != nil {
				t.Fatal(err)
			}
		}
		out, err := s.Result(d)
		if err != nil || intervalInfinity(out) != intervalInfinity(infinity) {
			t.Fatal(out, err)
		}
		if s.count != 1 {
			t.Fatal("infinity or NULL counted as finite")
		}
	}
	var mixed IntervalAvgState
	mixed.Add(positive)
	mixed.Add(negative)
	_, err = mixed.Result(d)
	intervalAvgAssertRange(t, err)
	var afterInfinity IntervalAvgState
	afterInfinity.Add(positive)
	maximum, _ := IntervalValue(d, math.MaxInt32, 0, 0)
	afterInfinity.Add(maximum)
	one, _ := IntervalValue(d, 1, 0, 0)
	before := afterInfinity
	intervalAvgAssertRange(t, afterInfinity.Add(one))
	if afterInfinity != before {
		t.Fatal("overflow after infinity changed state")
	}
	var other IntervalAvgState
	other.Add(finite)
	out, err := other.Result(d)
	if err != nil || out.IsNull() || empty.count != 0 {
		t.Fatal("state isolation", out, err)
	}
	for _, invalid := range []Value{{}, Null(types[Int4])} {
		err := other.Add(invalid)
		var state interface{ SQLState() string }
		if err == nil || errors.As(err, &state) {
			t.Fatal("invalid datum must be ordinary error", err)
		}
	}
	for _, invalid := range []*Descriptor{nil, types[Int4]} {
		_, err := other.Result(invalid)
		var state interface{ SQLState() string }
		if err == nil || errors.As(err, &state) {
			t.Fatal("invalid descriptor must be ordinary error", err)
		}
	}
	var absent *IntervalAvgState
	if err := absent.Add(finite); err == nil {
		t.Fatal("nil Add state accepted")
	}
	if _, err := absent.Result(d); err == nil {
		t.Fatal("nil Result state accepted")
	}
}
