package sqltype

import (
	"context"
	"errors"
	"math"
	"testing"

	"stuntgres/internal/sqlstate"
)

func timestampSeriesUnit(t *testing.T, start, stop int64, months, days int32, micros int64) TimestampSeriesCursor {
	t.Helper()
	types := testTypes()
	s, err := OpenTimestampSeries(context.Background(), Value{typ: types[Timestamp], integer: start}, Value{typ: types[Timestamp], integer: stop}, Value{typ: types[Interval], months: months, days: days, integer: micros})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestTimestampSeriesCursor(t *testing.T) {
	for _, tc := range []struct {
		start, stop, step int64
		want              []int64
	}{
		{0, 4, 2, []int64{0, 2, 4}}, {4, 0, -2, []int64{4, 2, 0}}, {0, 5, 2, []int64{0, 2, 4}}, {1, 0, 1, nil}, {0, 1, -1, nil},
	} {
		s := timestampSeriesUnit(t, tc.start, tc.stop, 0, 0, tc.step)
		for _, want := range tc.want {
			v, ok, err := s.Next(context.Background())
			if err != nil || !ok || v.integer != want {
				t.Fatal(v, ok, err, want)
			}
		}
		v, ok, err := s.Next(context.Background())
		if err != nil || ok || !v.null || v.typ != s.typ {
			t.Fatal(v, ok, err)
		}
	}
	// Jan31 2000 -> leap Feb29 -> Mar29, independently fixed epoch days.
	s := timestampSeriesUnit(t, 30*microsPerDay, 90*microsPerDay, 1, 0, 0)
	for _, day := range []int64{30, 59, 88} {
		v, ok, err := s.Next(context.Background())
		if err != nil || !ok || v.integer != day*microsPerDay {
			t.Fatal(v, ok, err, day)
		}
	}
	for _, infinity := range []int64{TimestampNegativeInfinity, TimestampPositiveInfinity} {
		s := timestampSeriesUnit(t, infinity, infinity, 0, 1, 0)
		for range 3 {
			v, ok, err := s.Next(context.Background())
			if err != nil || !ok || v.integer != infinity {
				t.Fatal(v, ok, err)
			}
		}
		s.Close()
	}
}

func TestTimestampSeriesSign(t *testing.T) {
	types := testTypes()
	for _, tc := range []struct {
		months, days int32
		micros       int64
		direction    int
	}{
		{1, -30, 0, 0}, {0, 1, -microsPerDay, 0}, {1, -29, 0, 1}, {-1, 29, 0, -1},
		{math.MaxInt32, math.MinInt32, math.MinInt64, 1}, {math.MinInt32, math.MaxInt32, math.MaxInt64, -1}, {0, 0, -1, -1},
	} {
		s, err := OpenTimestampSeries(context.Background(), Value{typ: types[Timestamp]}, Value{typ: types[Timestamp]}, Value{typ: types[Interval], months: tc.months, days: tc.days, integer: tc.micros})
		if tc.direction == 0 {
			var se *sqlstate.Error
			if !errors.As(err, &se) || se.Code != "22023" || se.Message != "step size cannot equal zero" {
				t.Fatal(err)
			}
		} else if err != nil || s.direction != tc.direction {
			t.Fatal(s.direction, err)
		}
	}
	for _, sign := range []int{1, -1} {
		step := Value{typ: types[Interval], months: math.MaxInt32, days: math.MaxInt32, integer: math.MaxInt64}
		if sign < 0 {
			step.months = math.MinInt32
			step.days = math.MinInt32
			step.integer = math.MinInt64
		}
		_, err := OpenTimestampSeries(context.Background(), Value{typ: types[Timestamp]}, Value{typ: types[Timestamp]}, step)
		var se *sqlstate.Error
		if !errors.As(err, &se) || se.Code != "22023" || se.Message != "step size cannot be infinite" {
			t.Fatal(err)
		}
		for _, which := range []int{0, 1, 2} {
			args := [3]Value{{typ: types[Timestamp]}, {typ: types[Timestamp]}, step}
			args[which] = Null(args[which].typ)
			s, err := OpenTimestampSeries(context.Background(), args[0], args[1], args[2])
			if err != nil {
				t.Fatal(err)
			}
			value, ok, err := s.Next(context.Background())
			if err != nil || ok || !value.null || value.typ != types[Timestamp] {
				t.Fatal(value, ok, err)
			}
		}
	}
	// Positive net30-day sign does not promise positive civil progress.
	s := timestampSeriesUnit(t, 396*microsPerDay, 424*microsPerDay, 1, -29, 0)
	for _, day := range []int64{396, 395, 395} {
		v, ok, err := s.Next(context.Background())
		if err != nil || !ok || v.integer != day*microsPerDay {
			t.Fatal(v, ok, err, day)
		}
	}
}

func TestTimestampSeriesOverflow(t *testing.T) {
	for _, tc := range []struct {
		start, stop, step int64
		prefix            bool
	}{
		{timestampEnd - 1, timestampEnd - 1, 1, false}, {timestampMinimum, timestampMinimum, -1, false}, {timestampEnd - 2, timestampEnd - 1, 1, true},
	} {
		s := timestampSeriesUnit(t, tc.start, tc.stop, 0, 0, tc.step)
		if tc.prefix {
			if _, ok, err := s.Next(context.Background()); !ok || err != nil {
				t.Fatal(ok, err)
			}
		}
		_, ok, err := s.Next(context.Background())
		var se *sqlstate.Error
		if ok || !errors.As(err, &se) || se.Code != "22008" || se.Message != "timestamp out of range" || !s.exhausted {
			t.Fatal(ok, err)
		}
	}
}

func TestTimestampSeriesLifecycle(t *testing.T) {
	s := timestampSeriesUnit(t, 0, 2, 0, 0, 1)
	v, ok, err := s.Next(context.Background())
	if err != nil || !ok {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok, err := s.Next(ctx); ok || !errors.Is(err, context.Canceled) || !s.exhausted {
		t.Fatal(ok, err)
	}
	s.Close()
	s.Close()
	if v.integer != 0 || v.typ != s.typ {
		t.Fatal("mutated publication")
	}
	eof, ok, err := s.Next(context.Background())
	if err != nil || ok || !eof.null || eof.typ != v.typ {
		t.Fatal(eof, ok, err)
	}
	if _, _, err := s.Next(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := OpenTimestampSeries(ctx, v, v, Value{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	other := timestampSeriesUnit(t, 1, 1, 0, 0, 1)
	if value, ok, err := other.Next(context.Background()); err != nil || !ok || value.integer != 1 {
		t.Fatal(value, ok, err)
	}
}
