package sqltype

import (
	"context"
	"errors"
	"math"
	"reflect"
	"stuntgres/internal/sqlstate"
	"testing"
)

func datetimeMakeDescriptors() map[ID]*Descriptor {
	d := make(map[ID]*Descriptor)
	for _, typ := range Builtins() {
		d[typ.ID()] = typ
	}
	return d
}
func datetimeMakeValues(t *testing.T, d map[ID]*Descriptor, ints []int64, sec *float64, zone *string) []Value {
	t.Helper()
	values := make([]Value, 0, 7)
	for _, n := range ints {
		v, err := Integer(d[Int4], n)
		if err != nil {
			t.Fatal(err)
		}
		values = append(values, v)
	}
	if sec != nil {
		v, err := Float(d[Float8], *sec)
		if err != nil {
			t.Fatal(err)
		}
		values = append(values, v)
	}
	if zone != nil {
		values = append(values, String(d[Text], *zone))
	}
	return values
}
func datetimeMakeEvaluate(t *testing.T, name string, result ID, ints []int64, seconds float64, zone *string, env InputEnvironment) (Value, error) {
	t.Helper()
	d := datetimeMakeDescriptors()
	var sec *float64
	if name != "make_date" {
		sec = &seconds
	}
	values := datetimeMakeValues(t, d, ints, sec, zone)
	p, err := BindDatetimeMake(name, d[result], len(values))
	if err != nil {
		t.Fatal(err)
	}
	return p.Evaluate(context.Background(), values, env)
}

func TestDatetimeMakeCalendar(t *testing.T) {
	for _, tc := range []struct{ year, month, day, want int64 }{{2000, 1, 1, 0}, {1970, 1, 1, -10957}, {2000, 2, 29, 59}, {-4714, 11, 24, -2451545}, {5874897, 12, 31, 2145031948}} {
		v, err := datetimeMakeEvaluate(t, "make_date", Date, []int64{tc.year, tc.month, tc.day}, 0, nil, InputEnvironment{})
		if err != nil {
			t.Fatal(tc, err)
		}
		got, err := v.Date()
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	for _, tc := range []struct {
		ints []int64
		sec  float64
		want int64
	}{
		{[]int64{-4714, 11, 24, 0, 0}, 0, -211813488000000000},
		{[]int64{-4714, 11, 23, 24, 0}, 0, -211813488000000000},
		{[]int64{1999, 12, 31, 24, 0}, 0, 0},
		{[]int64{294276, 12, 31, 23, 59}, 59.999999, 9223371331199999999},
	} {
		v, err := datetimeMakeEvaluate(t, "make_timestamp", Timestamp, tc.ints, tc.sec, nil, InputEnvironment{})
		if err != nil {
			t.Fatal(tc, err)
		}
		n, err := v.Timestamp()
		if err != nil || n != tc.want {
			t.Fatal(tc, n, err)
		}
	}
}

func TestDatetimeMakeClock(t *testing.T) {
	for _, tc := range []struct {
		hour, minute int64
		sec          float64
		want         int64
	}{
		{24, 0, 0, 86400000000}, {12, 34, 60, 45300000000}, {0, 0, 0.0000005, 0}, {0, 0, 0.0000015, 2}, {23, 59, 59.9999995, 86400000000}, {0, 0, -0.0000004, 0}, {24, 0, 0.0000004, 86400000000},
	} {
		v, err := datetimeMakeEvaluate(t, "make_time", Time, []int64{tc.hour, tc.minute}, tc.sec, nil, InputEnvironment{})
		if err != nil {
			t.Fatal(tc, err)
		}
		n, err := v.Time()
		if err != nil || n != tc.want {
			t.Fatal(tc, n, err)
		}
	}
	for _, tc := range []struct {
		hour, minute int64
		sec          float64
	}{{-1, 0, 0}, {25, 0, 0}, {0, -1, 0}, {0, 60, 0}, {0, 0, -1}, {0, 0, 61}, {24, 0, 0.000001}, {23, 59, 60.000001}, {0, 0, math.NaN()}, {0, 0, math.Inf(1)}, {0, 0, math.Inf(-1)}} {
		_, err := datetimeMakeEvaluate(t, "make_time", Time, []int64{tc.hour, tc.minute}, tc.sec, nil, InputEnvironment{})
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "22008" {
			t.Fatal(tc, err)
		}
	}
}

func TestDatetimeMakeInterval(t *testing.T) {
	for _, tc := range []struct {
		ints         []int64
		sec          float64
		months, days int32
		micros       int64
	}{
		{[]int64{0, 0, 0, 0, 0, 0}, 0, 0, 0, 0}, {[]int64{1, 2, 3, 4, 5, 6}, 7, 14, 25, 18367000000},
		{[]int64{1, -1, 5, -7, 25, -180}, 0, 11, 28, 79200000000},
		{[]int64{178956970, 0, 0, 0, 0, 0}, 0, 2147483640, 0, 0},
		{[]int64{0, 0, 306783378, 0, 0, 0}, 0, 0, 2147483646, 0},
		{[]int64{0, 0, 0, 0, 24, 0}, 0, 0, 0, 86400000000},
		{[]int64{0, 0, 0, 0, 0, 0}, -9223372036854.775808, 0, 0, math.MinInt64},
	} {
		v, err := datetimeMakeEvaluate(t, "make_interval", Interval, tc.ints, tc.sec, nil, InputEnvironment{})
		if err != nil {
			t.Fatal(tc, err)
		}
		m, d, n, err := v.Interval()
		if err != nil || m != tc.months || d != tc.days || n != tc.micros {
			t.Fatal(tc, m, d, n, err)
		}
	}
	for _, tc := range []struct {
		ints []int64
		sec  float64
		code string
	}{
		{[]int64{178956971, -12, 0, 0, 0, 0}, 0, "22008"}, {[]int64{-178956971, 12, 0, 0, 0, 0}, 0, "22008"},
		{[]int64{0, 0, 306783379, -7, 0, 0}, 0, "22008"}, {[]int64{0, 0, 0, 0, 0, 0}, 9223372036854.775808, "22008"},
		{[]int64{0, 0, 0, 0, math.MaxInt32, 0}, 9223372036854, "22008"},
		{[]int64{0, 0, 0, 0, 0, 0}, math.NaN(), "22008"}, {[]int64{0, 0, 0, 0, 0, 0}, math.Inf(1), "22008"},
		{[]int64{0, 0, 0, 0, 0, 0}, 1e308, "22003"},
	} {
		_, err := datetimeMakeEvaluate(t, "make_interval", Interval, tc.ints, tc.sec, nil, InputEnvironment{})
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != tc.code {
			t.Fatal(tc, err)
		}
	}
}

func TestDatetimeMakeZone(t *testing.T) {
	utc, err := ResolveZone("UTC")
	if err != nil {
		t.Fatal(err)
	}
	ny, err := ResolveZone("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		ints []int64
		zone string
		want int64
	}{
		{[]int64{2000, 1, 1, 12, 0}, "+2", 36000000000}, {[]int64{2000, 1, 1, 12, 0}, "UTC+2", 50400000000},
		{[]int64{2000, 1, 1, 12, 0}, "+1:", 39600000000}, {[]int64{2000, 1, 1, 12, 0}, "+100", 39600000000},
		{[]int64{2000, 1, 1, 12, 0}, "+1000", 7200000000}, {[]int64{2000, 1, 1, 12, 0}, "+10:00:", 7200000000},
	} {
		v, err := datetimeMakeEvaluate(t, "make_timestamptz", Timestamptz, tc.ints, 0, &tc.zone, InputEnvironment{Zone: utc})
		if err != nil {
			t.Fatal(tc, err)
		}
		n, err := v.Timestamp()
		if err != nil || n != tc.want {
			t.Fatal(tc, n, err)
		}
	}
	for _, tc := range []struct {
		zone *Zone
		want int64
	}{{utc, 43200000000}, {ny, 61200000000}} {
		v, err := datetimeMakeEvaluate(t, "make_timestamptz", Timestamptz, []int64{2000, 1, 1, 12, 0}, 0, nil, InputEnvironment{Zone: tc.zone})
		if err != nil {
			t.Fatal(err)
		}
		n, err := v.Timestamp()
		if err != nil || n != tc.want {
			t.Fatal(n, err)
		}
	}
}

func TestDatetimeMakeDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name          string
		result        ID
		ints          []int64
		sec           float64
		zone          *string
		message, code string
	}{
		{"make_date", Date, []int64{-44, 2, 30}, 0, nil, "date field value out of range: -43-02-30", "22008"},
		{"make_date", Date, []int64{math.MinInt32, 1, 1}, 0, nil, "date field value out of range: -2147483648-01-01", "22008"},
		{"make_timestamp", Timestamp, []int64{-44, 2, 30, 0, 0}, 0, nil, "date field value out of range: -44-02-30", "22008"},
		{"make_timestamp", Timestamp, []int64{294276, 12, 31, 23, 59}, 59.9999995, nil, "timestamp out of range: 294276-12-31 23:59:60", "22008"},
		{"make_time", Time, []int64{0, 0}, math.NaN(), nil, "time field value out of range: 0:00:NaN", "22008"},
		{"make_interval", Interval, []int64{0, 0, 0, 0, 0, 0}, 1e308, nil, "value out of range: overflow", "22003"},
	} {
		_, err := datetimeMakeEvaluate(t, tc.name, tc.result, tc.ints, tc.sec, tc.zone, InputEnvironment{})
		var d *sqlstate.Error
		want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message}
		if !errors.As(err, &d) || !reflect.DeepEqual(d, want) {
			t.Fatal(tc, err, want)
		}
	}
	d := datetimeMakeDescriptors()
	p, err := BindDatetimeMake("make_interval", d[Interval], 7)
	if err != nil {
		t.Fatal(err)
	}
	sec := float64(0)
	args := datetimeMakeValues(t, d, []int64{0, 0, 0, 0, 0, 0}, &sec, nil)
	for i := range args {
		saved := args[i]
		args[i] = Null(saved.Type())
		v, err := p.Evaluate(context.Background(), args, InputEnvironment{})
		if err != nil || !v.IsNull() || v.Type() != d[Interval] {
			t.Fatal(i, v, err)
		}
		args[i] = saved
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Evaluate(ctx, args, InputEnvironment{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestDatetimeMakeAllocations(t *testing.T) {
	d := datetimeMakeDescriptors()
	sec := 23.5
	for _, tc := range []struct {
		name    string
		id      ID
		ints    []int64
		seconds *float64
	}{{"make_date", Date, []int64{2013, 7, 15}, nil}, {"make_time", Time, []int64{8, 15}, &sec}, {"make_timestamp", Timestamp, []int64{2013, 7, 15, 8, 15}, &sec}, {"make_interval", Interval, []int64{1, 2, 3, 4, 5, 6}, &sec}} {
		args := datetimeMakeValues(t, d, tc.ints, tc.seconds, nil)
		p, err := BindDatetimeMake(tc.name, d[tc.id], len(args))
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		var failure error
		allocations := testing.AllocsPerRun(1000, func() { _, failure = p.Evaluate(ctx, args, InputEnvironment{}) })
		if failure != nil || allocations != 0 {
			t.Fatal(tc.name, allocations, failure)
		}
	}
}
