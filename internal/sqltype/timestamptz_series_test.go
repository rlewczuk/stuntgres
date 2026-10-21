package sqltype

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func timestamptzSeriesUnit(t *testing.T, start, stop int64, months, days int32, micros int64, input InputEnvironment, zone ...string) TimestampSeriesCursor {
	t.Helper()
	types := testTypes()
	var timezone []Value
	if len(zone) > 0 {
		timezone = []Value{String(types[Text], zone[0])}
	}
	s, err := OpenTimestamptzSeries(context.Background(), Value{typ: types[Timestamptz], integer: start}, Value{typ: types[Timestamptz], integer: stop}, Value{typ: types[Interval], months: months, days: days, integer: micros}, input, timezone...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func timestamptzSeriesRead(t *testing.T, s *TimestampSeriesCursor, want []int64, exhausted bool) {
	t.Helper()
	for _, n := range want {
		v, ok, err := s.Next(context.Background())
		if err != nil || !ok || v.typ != s.typ || v.integer != n {
			t.Fatal(v, ok, err, n)
		}
	}
	if exhausted {
		v, ok, err := s.Next(context.Background())
		if err != nil || ok || !v.null || v.typ != s.typ {
			t.Fatal("typed EOF", v, ok, err)
		}
	}
}

func TestTimestamptzSeriesCursor(t *testing.T) {
	for _, tc := range []struct {
		start, stop, step int64
		want              []int64
	}{
		{0, 4, 2, []int64{0, 2, 4}}, {4, 0, -2, []int64{4, 2, 0}}, {0, 5, 2, []int64{0, 2, 4}}, {1, 0, 1, nil}, {0, 1, -1, nil}, {1, 1, 1, []int64{1}}, {1, 1, -1, []int64{1}},
	} {
		s := timestamptzSeriesUnit(t, tc.start, tc.stop, 0, 0, tc.step, InputEnvironment{}, "UTC")
		timestamptzSeriesRead(t, &s, tc.want, true)
	}
	for _, infinity := range []int64{TimestampNegativeInfinity, TimestampPositiveInfinity} {
		s := timestamptzSeriesUnit(t, infinity, infinity, 0, 1, 0, InputEnvironment{}, "America/New_York")
		timestamptzSeriesRead(t, &s, []int64{infinity, infinity, infinity}, false)
		s.Close()
	}
}

func TestTimestamptzSeriesCalendar(t *testing.T) {
	const hour = int64(3600000000)
	// Independent UTC epoch-day constants (2000-01-01 origin), not civil conversion.
	for _, tc := range []struct {
		start, stop  int64
		months, days int32
		micros       int64
		want         []int64
	}{
		{8834*microsPerDay + 17*hour, 8836*microsPerDay + 17*hour, 0, 1, 0, []int64{8834*microsPerDay + 17*hour, 8835*microsPerDay + 16*hour, 8836*microsPerDay + 16*hour}},
		{8834*microsPerDay + 17*hour, 8836*microsPerDay + 17*hour, 0, 0, 24 * hour, []int64{8834*microsPerDay + 17*hour, 8835*microsPerDay + 17*hour, 8836*microsPerDay + 17*hour}},
		{8796*microsPerDay + 17*hour, 8856*microsPerDay + 16*hour, 1, 0, 0, []int64{8796*microsPerDay + 17*hour, 8825*microsPerDay + 17*hour, 8854*microsPerDay + 16*hour}},
		{8834*microsPerDay + 7*hour + hour/2, 8836*microsPerDay + 8*hour, 0, 1, 0, []int64{8834*microsPerDay + 7*hour + hour/2, 8835*microsPerDay + 7*hour + hour/2, 8836*microsPerDay + 7*hour + hour/2}},
		{9072*microsPerDay + 5*hour + hour/2, 9074*microsPerDay + 7*hour, 0, 1, 0, []int64{9072*microsPerDay + 5*hour + hour/2, 9073*microsPerDay + 6*hour + hour/2, 9074*microsPerDay + 6*hour + hour/2}},
		{8836*microsPerDay + 16*hour, 8834*microsPerDay + 17*hour, 0, -1, 0, []int64{8836*microsPerDay + 16*hour, 8835*microsPerDay + 16*hour, 8834*microsPerDay + 17*hour}},
		{8795*microsPerDay + 17*hour, 8826*microsPerDay + 18*hour, 1, 1, hour, []int64{8795*microsPerDay + 17*hour, 8826*microsPerDay + 18*hour}},
	} {
		s := timestamptzSeriesUnit(t, tc.start, tc.stop, tc.months, tc.days, tc.micros, InputEnvironment{}, "America/New_York")
		timestamptzSeriesRead(t, &s, tc.want, true)
	}
	// Positive sign can stall across spring DST; no monotonicity guard.
	s := timestamptzSeriesUnit(t, 8834*microsPerDay+17*hour, 8836*microsPerDay+16*hour, 0, 1, -23*hour, InputEnvironment{}, "America/New_York")
	timestamptzSeriesRead(t, &s, []int64{8834*microsPerDay + 17*hour, 8834*microsPerDay + 17*hour, 8834*microsPerDay + 17*hour}, false)
}

func TestTimestamptzSeriesZones(t *testing.T) {
	const hour = int64(3600000000)
	ny, err := ResolveZone("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	start, stop := int64(8834)*microsPerDay+17*hour, int64(8836)*microsPerDay+17*hour
	session := timestamptzSeriesUnit(t, start, stop, 0, 1, 0, InputEnvironment{Zone: ny})
	timestamptzSeriesRead(t, &session, []int64{start, 8835*microsPerDay + 16*hour, 8836*microsPerDay + 16*hour}, true)
	for _, tc := range []struct {
		session *Zone
		set     TimezoneAbbreviations
		name    string
		dst     bool
	}{
		{nil, TimezoneAbbreviationsDefault, "america/new_york", true},
		{nil, TimezoneAbbreviationsDefault, "US/Eastern", true},
		{nil, TimezoneAbbreviationsDefault, "EST5EDT", true},
		{nil, TimezoneAbbreviationsDefault, "EST", false},
		{ny, TimezoneAbbreviationsAustralia, "EST", false},
		{nil, TimezoneAbbreviationsIndia, "IST", false},
		{nil, TimezoneAbbreviationsDefault, strings.Repeat("A", 254) + "0BAD", false},
	} {
		s := timestamptzSeriesUnit(t, start, stop, 0, 1, 0, InputEnvironment{Zone: tc.session, TimezoneAbbreviations: tc.set}, tc.name)
		want := []int64{start, 8835*microsPerDay + 17*hour, 8836*microsPerDay + 17*hour}
		if tc.dst {
			want = []int64{start, 8835*microsPerDay + 16*hour, 8836*microsPerDay + 16*hour}
		}
		// Copying and closing the original cannot invalidate an inline fixed zone.
		copied := s
		s.Close()
		timestamptzSeriesRead(t, &copied, want, true)
	}
	lh := timestamptzSeriesUnit(t, 8862*microsPerDay+hour, 8864*microsPerDay+2*hour, 0, 1, 0, InputEnvironment{}, "LHDT")
	timestamptzSeriesRead(t, &lh, []int64{8862*microsPerDay + hour, 8863*microsPerDay + hour + hour/2, 8864*microsPerDay + hour + hour/2}, true)
}

func TestTimestamptzSeriesSign(t *testing.T) {
	types := testTypes()
	start := Value{typ: types[Timestamptz]}
	for _, tc := range []struct {
		months, days int32
		micros       int64
		direction    int
	}{
		{1, -30, 0, 0}, {0, 1, -microsPerDay, 0}, {1, -29, 0, 1}, {-1, 29, 0, -1},
		{math.MaxInt32, math.MinInt32, math.MinInt64, 1}, {math.MinInt32, math.MaxInt32, math.MaxInt64, -1},
	} {
		step := Value{typ: types[Interval], months: tc.months, days: tc.days, integer: tc.micros}
		s, err := OpenTimestamptzSeries(context.Background(), start, start, step, InputEnvironment{})
		if tc.direction == 0 {
			timestamptzSeriesError(t, err, "22023", "step size cannot equal zero")
		} else if err != nil || s.direction != tc.direction {
			t.Fatal(s, err)
		}
	}
	for _, step := range []Value{
		{typ: types[Interval], months: math.MaxInt32, days: math.MaxInt32, integer: math.MaxInt64},
		{typ: types[Interval], months: math.MinInt32, days: math.MinInt32, integer: math.MinInt64},
	} {
		_, err := OpenTimestamptzSeries(context.Background(), start, start, step, InputEnvironment{})
		timestamptzSeriesError(t, err, "22023", "step size cannot be infinite")
	}
	for _, step := range []Value{{typ: types[Interval]}, {typ: types[Interval], months: math.MaxInt32, days: math.MaxInt32, integer: math.MaxInt64}} {
		args := [4]Value{start, start, step, String(types[Text], "No/Such_Zone")}
		_, err := OpenTimestamptzSeries(context.Background(), args[0], args[1], args[2], InputEnvironment{}, args[3])
		timestamptzSeriesError(t, err, "22023", `time zone "No/Such_Zone" not recognized`)
		for i := range args {
			copy := args
			copy[i] = Null(copy[i].typ)
			s, err := OpenTimestamptzSeries(context.Background(), copy[0], copy[1], copy[2], InputEnvironment{}, copy[3])
			if err != nil {
				t.Fatal(err)
			}
			timestamptzSeriesRead(t, &s, nil, true)
		}
	}
}

func timestamptzSeriesError(t *testing.T, err error, code, message string) {
	t.Helper()
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != code || diagnostic.Message != message || diagnostic.Position != "" {
		t.Fatal(err, code, message)
	}
}

func TestTimestamptzSeriesOverflow(t *testing.T) {
	for _, tc := range []struct {
		start, stop  int64
		months, days int32
		micros       int64
		prefix       bool
	}{
		{timestampEnd - 1, timestampEnd - 1, 0, 0, 1, false},
		{timestampMinimum, timestampMinimum, 0, 0, -1, false},
		{timestampEnd - 2, timestampEnd - 1, 0, 0, 1, true},
		{timestampEnd - 1, timestampEnd - 1, 1, 0, 0, false},
		{timestampEnd - 1, timestampEnd - 1, 0, 1, 0, false},
	} {
		s := timestamptzSeriesUnit(t, tc.start, tc.stop, tc.months, tc.days, tc.micros, InputEnvironment{}, "UTC")
		if tc.prefix {
			timestamptzSeriesRead(t, &s, []int64{tc.start}, false)
		}
		v, ok, err := s.Next(context.Background())
		if ok || v.typ != nil || !s.exhausted {
			t.Fatal("published overflow row", v, ok, err)
		}
		timestamptzSeriesError(t, err, "22008", "timestamp out of range")
		timestamptzSeriesRead(t, &s, nil, true)
	}
	s := timestamptzSeriesUnit(t, timestampEnd-1, 0, 0, 0, 1, InputEnvironment{}, "UTC")
	timestamptzSeriesRead(t, &s, nil, true)
}

func TestTimestamptzSeriesLifecycle(t *testing.T) {
	s := timestamptzSeriesUnit(t, 0, 3, 0, 0, 1, InputEnvironment{}, "EST")
	published, ok, err := s.Next(context.Background())
	if err != nil || !ok {
		t.Fatal(err)
	}
	// Next polls once before work and again after computing the successor.
	ctx := &mathCheckpointContext{Context: context.Background(), remaining: 2}
	value, ok, err := s.Next(ctx)
	if !errors.Is(err, context.Canceled) || ok || value.typ != nil || !s.exhausted || ctx.remaining != 0 {
		t.Fatal(value, ok, err)
	}
	s.Close()
	s.Close()
	if published.integer != 0 || published.typ != s.typ {
		t.Fatal("mutated publication")
	}
	timestamptzSeriesRead(t, &s, nil, true)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok, err := s.Next(cancelled); ok || !errors.Is(err, context.Canceled) {
		t.Fatal(ok, err)
	}
	if _, err := OpenTimestamptzSeries(cancelled, Value{}, Value{}, Value{}, InputEnvironment{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	types := testTypes()
	v := Value{typ: types[Timestamptz]}
	step := Value{typ: types[Interval], integer: 1}
	for _, zones := range [][]Value{{String(types[Text], "UTC"), String(types[Text], "UTC")}, {{typ: types[Int4]}}} {
		if _, err := OpenTimestamptzSeries(context.Background(), v, v, step, InputEnvironment{}, zones...); err == nil {
			t.Fatal("invalid timezone shape admitted")
		}
	}
}
