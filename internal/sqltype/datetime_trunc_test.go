package sqltype

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestDatetimeTruncDecoder(t *testing.T) {
	for _, tc := range []struct{ aliases, unit string }{
		{"microseconds microsecond us usec usecond useconds usecs MICROSECONDS microseconds_extra", "microseconds"},
		{"milliseconds millisecond ms msec msecond mseconds msecs", "milliseconds"},
		{"s sec second seconds secs", "second"}, {"m min mins minute minutes", "minute"}, {"h hour hours hr hrs", "hour"},
		{"d day days", "day"}, {"w week weeks", "week"}, {"mon mons month months", "month"}, {"qtr quarter", "quarter"},
		{"y year years yr yrs", "year"}, {"dec decade decades decs", "decade"}, {"c cent centuries century", "century"},
		{"mil millennia millennium millenniums mils", "millennium"}, {"timezone", "timezone"}, {"timezone_hour", "timezone_hour"}, {"timezone_minute", "timezone_minute"},
	} {
		for _, alias := range strings.Fields(tc.aliases) {
			_, unit := datetimeTruncUnit(alias)
			if unit != tc.unit {
				t.Fatal(alias, unit, tc.unit)
			}
		}
	}
	for _, field := range strings.Fields("mm epoch dow doy isodow isoyear j jd julian ago @ quarters q nanoseconds") {
		if _, unit := datetimeTruncUnit(field); unit != "" {
			t.Fatal(field, unit)
		}
	}
	if normalized, unit := datetimeTruncUnit(strings.Repeat("É", 40)); normalized != strings.Repeat("É", 31) || unit != "" {
		t.Fatal(normalized, unit)
	}
	if _, unit := datetimeTruncUnit(" day"); unit != "" {
		t.Fatal(unit)
	}
}

func TestDatetimeTruncCalendar(t *testing.T) {
	d := datetimeMakeDescriptors()
	p, err := BindDatetimeTrunc(d[Text], d[Timestamp], d[Timestamp], nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		unit                       string
		y, m, day, h, min, sec, us int
	}{
		{"microseconds", 2001, 2, 16, 20, 38, 40, 123456}, {"milliseconds", 2001, 2, 16, 20, 38, 40, 123000},
		{"second", 2001, 2, 16, 20, 38, 40, 0}, {"minute", 2001, 2, 16, 20, 38, 0, 0}, {"hour", 2001, 2, 16, 20, 0, 0, 0},
		{"day", 2001, 2, 16, 0, 0, 0, 0}, {"week", 2001, 2, 12, 0, 0, 0, 0}, {"month", 2001, 2, 1, 0, 0, 0, 0},
		{"quarter", 2001, 1, 1, 0, 0, 0, 0}, {"year", 2001, 1, 1, 0, 0, 0, 0}, {"decade", 2000, 1, 1, 0, 0, 0, 0},
		{"century", 2001, 1, 1, 0, 0, 0, 0}, {"millennium", 2001, 1, 1, 0, 0, 0, 0},
	} {
		n, _ := civilTimestamp(2001, 2, 16, 20, 38, 40, 123456)
		input, _ := TimestampValue(d[Timestamp], n)
		field := String(d[Text], tc.unit)
		v, err := p.Evaluate(context.Background(), []Value{field, input}, InputEnvironment{})
		want, _ := civilTimestamp(tc.y, tc.m, tc.day, tc.h, tc.min, tc.sec, tc.us)
		if err != nil || v.integer != want || v.typ != d[Timestamp] || input.integer != n {
			t.Fatal(tc, v, err, want)
		}
	}
	for _, tc := range []struct {
		unit    string
		y, want int
	}{
		{"decade", 1, 0}, {"decade", 9, 0}, {"decade", 10, 10}, {"decade", 0, 0}, {"decade", -1, -10}, {"decade", -10, -10},
		{"century", 99, 1}, {"century", 100, 1}, {"century", 101, 101}, {"century", 0, -99}, {"century", -99, -99}, {"century", -100, -199},
		{"millennium", 1000, 1}, {"millennium", 1001, 1001}, {"millennium", 0, -999}, {"millennium", -999, -999}, {"millennium", -1000, -1999},
	} {
		n, _ := civilTimestamp(tc.y, 6, 15, 12, 0, 0, 0)
		input, _ := TimestampValue(d[Timestamp], n)
		field := String(d[Text], tc.unit)
		v, err := p.Evaluate(context.Background(), []Value{field, input}, InputEnvironment{})
		want, _ := civilTimestamp(tc.want, 1, 1, 0, 0, 0, 0)
		if err != nil || v.integer != want {
			t.Fatal(tc, v, err, want)
		}
	}
	field := String(d[Text], "milliseconds")
	n, _ := civilTimestamp(1999, 12, 31, 23, 59, 59, 999999)
	input, _ := TimestampValue(d[Timestamp], n)
	v, err := p.Evaluate(context.Background(), []Value{field, input}, InputEnvironment{})
	if err != nil || v.integer != -1000 {
		t.Fatal(v, err)
	}
}

func TestDatetimeTruncIntervals(t *testing.T) {
	d := datetimeMakeDescriptors()
	p, _ := BindDatetimeTrunc(d[Text], d[Interval], d[Interval], nil)
	for _, tc := range []struct {
		unit   string
		m, day int32
		clock  int64
		wm, wd int32
		wt     int64
	}{
		{"quarter", -16, 3, -1999999, -15, 0, 0}, {"year", 14, 3, 1, 12, 0, 0}, {"month", 14, -3, -1, 14, 0, 0},
		{"day", 14, -3, -1, 14, -3, 0}, {"hour", 1, -2, 49*3600000000 + 300000000, 1, -2, 49 * 3600000000},
		{"second", 1, -2, -1999999, 1, -2, -1000000}, {"milliseconds", 1, -2, -1999999, 1, -2, -1999000},
		{"microseconds", math.MaxInt32, math.MinInt32, math.MinInt64, math.MaxInt32, math.MinInt32, math.MinInt64},
		{"decade", math.MinInt32, 0, 1, -2147483640, 0, 0}, {"century", math.MaxInt32, 0, 1, 2147482800, 0, 0},
	} {
		field := String(d[Text], tc.unit)
		input, _ := IntervalValue(d[Interval], tc.m, tc.day, tc.clock)
		v, err := p.Evaluate(context.Background(), []Value{field, input}, InputEnvironment{})
		if err != nil || v.months != tc.wm || v.days != tc.wd || v.integer != tc.wt {
			t.Fatal(tc, v, err)
		}
	}
}

func TestDatetimeTruncZonesOwnershipErrors(t *testing.T) {
	d, other := datetimeMakeDescriptors(), datetimeMakeDescriptors()
	p, _ := BindDatetimeTrunc(d[Text], d[Timestamptz], d[Timestamptz], d[Text])
	utc, _ := ResolveZone("UTC")
	for _, tc := range []struct{ field, source, zone, want string }{
		{"hour", "2024-11-03 05:30+00", "America/New_York", "2024-11-03 05:00+00"},
		{"hour", "2024-11-03 06:30+00", "America/New_York", "2024-11-03 06:00+00"},
		{"day", "2024-03-10 12:00+00", "America/New_York", "2024-03-10 05:00+00"},
		{"hour", "2024-04-06 15:15+00", "Australia/Lord_Howe", "2024-04-06 14:30+00"},
		{"day", "2008-01-01 12:00+00", "VET", "2008-01-01 04:30+00"},
	} {
		field := String(d[Text], tc.field)
		zone := String(d[Text], tc.zone)
		input, err := d[Timestamptz].InputWithEnvironment(context.Background(), tc.source, -1, InputEnvironment{Zone: utc})
		if err != nil {
			t.Fatal(err)
		}
		want, err := d[Timestamptz].InputWithEnvironment(context.Background(), tc.want, -1, InputEnvironment{Zone: utc})
		if err != nil {
			t.Fatal(err)
		}
		v, err := p.Evaluate(context.Background(), []Value{field, input, zone}, InputEnvironment{Zone: utc})
		if err != nil || v.integer != want.integer {
			t.Fatal(tc, v, err, want)
		}
	}
	field := String(d[Text], "bad")
	zone := String(d[Text], "Not/AZone")
	input, _ := TimestampValue(d[Timestamptz], TimestampPositiveInfinity)
	_, err := p.Evaluate(context.Background(), []Value{field, input, zone}, InputEnvironment{Zone: utc})
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Message != `time zone "Not/AZone" not recognized` {
		t.Fatal(err)
	}
	v, err := p.Evaluate(context.Background(), []Value{field, Null(d[Timestamptz]), zone}, InputEnvironment{})
	if err != nil || !v.IsNull() || v.typ != d[Timestamptz] {
		t.Fatal(v, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Evaluate(ctx, []Value{field, input, zone}, InputEnvironment{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	next, err := p.Retag(func(desc *Descriptor) *Descriptor { return other[desc.ID()] })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := next.Evaluate(context.Background(), []Value{field, input, zone}, InputEnvironment{}); err == nil {
		t.Fatal("foreign descriptors accepted")
	}
	if _, err := BindDatetimeTrunc(d[Text], d[Timestamp], d[Timestamp], d[Text]); err == nil {
		t.Fatal("naive explicit zone accepted")
	}
	if _, err := PlanCoercion(d[Time].Usage(), d[Interval].Usage(), Implicit); err == nil {
		t.Fatal("general TIME cast changed")
	}
}

func TestDatetimeTruncBoundsInfinityTime(t *testing.T) {
	d := datetimeMakeDescriptors()
	p, _ := BindDatetimeTrunc(d[Text], d[Timestamp], d[Timestamp], nil)
	field := String(d[Text], "millennium")
	input, _ := TimestampValue(d[Timestamp], timestampMinimum)
	_, err := p.Evaluate(context.Background(), []Value{field, input}, InputEnvironment{})
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "22008" || diagnostic.Message != "timestamp out of range" {
		t.Fatal(err)
	}
	for _, id := range []ID{Timestamp, Timestamptz, Interval} {
		p, _ := BindDatetimeTrunc(d[Text], d[id], d[id], nil)
		for _, sign := range []int{-1, 1} {
			input := Value{typ: d[id], integer: TimestampPositiveInfinity}
			if sign < 0 {
				input.integer = TimestampNegativeInfinity
			}
			if id == Interval {
				if sign > 0 {
					input.months, input.days = math.MaxInt32, math.MaxInt32
				} else {
					input.months, input.days = math.MinInt32, math.MinInt32
				}
			}
			for _, unit := range strings.Fields("microseconds milliseconds second minute hour day month quarter year decade century millennium") {
				v, err := p.Evaluate(context.Background(), []Value{String(d[Text], unit), input}, InputEnvironment{})
				if err != nil || v != input {
					t.Fatal(id, sign, unit, v, err)
				}
			}
			_, err := p.Evaluate(context.Background(), []Value{String(d[Text], "bad"), input}, InputEnvironment{})
			if !errors.As(err, &diagnostic) || diagnostic.Code != "22023" {
				t.Fatal(id, err)
			}
			if id == Interval {
				_, err := p.Evaluate(context.Background(), []Value{String(d[Text], "week"), input}, InputEnvironment{})
				if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Detail != "Months usually have fractional weeks." {
					t.Fatal(err)
				}
			}
		}
	}
	timePlan, _ := BindDatetimeTrunc(d[Text], d[Time], d[Interval], nil)
	input = Value{typ: d[Time], integer: microsPerDay}
	for _, tc := range []struct {
		unit string
		want int64
	}{{"hour", microsPerDay}, {"day", 0}} {
		v, err := timePlan.Evaluate(context.Background(), []Value{String(d[Text], tc.unit), input}, InputEnvironment{})
		if err != nil || v.typ != d[Interval] || v.months != 0 || v.days != 0 || v.integer != tc.want {
			t.Fatal(tc, v, err)
		}
	}
	zone := FixedZone("east", 14*3600)
	zoned, _ := BindDatetimeTrunc(d[Text], d[Timestamptz], d[Timestamptz], nil)
	input, _ = TimestampValue(d[Timestamptz], timestampEnd-1)
	v, err := zoned.Evaluate(context.Background(), []Value{String(d[Text], "microseconds"), input}, InputEnvironment{Zone: zone})
	if err != nil || v != input {
		t.Fatal("near-limit civil shift", v, err)
	}
}

func TestDatetimeTruncCapturedAbbreviations(t *testing.T) {
	d := datetimeMakeDescriptors()
	p, err := BindDatetimeTrunc(d[Text], d[Timestamptz], d[Timestamptz], d[Text])
	if err != nil {
		t.Fatal(err)
	}
	n, _ := civilTimestamp(2004, 7, 1, 12, 0, 0, 0)
	source, _ := TimestampValue(d[Timestamptz], n)
	for _, tc := range []struct {
		session, token          string
		set                     TimezoneAbbreviations
		y, m, day, hour, minute int
	}{
		{"UTC", "EST", TimezoneAbbreviationsDefault, 2004, 7, 1, 5, 0},
		{"UTC", "EST", TimezoneAbbreviationsAustralia, 2004, 6, 30, 14, 0},
		{"America/New_York", "EST", TimezoneAbbreviationsAustralia, 2004, 7, 1, 5, 0},
		{"America/New_York", "CST", TimezoneAbbreviationsAustralia, 2004, 6, 30, 14, 30},
		{"UTC", "CST", TimezoneAbbreviationsDefault, 2004, 7, 1, 6, 0},
	} {
		zone, err := ResolveZone(tc.session)
		if err != nil {
			t.Fatal(err)
		}
		v, err := p.Evaluate(context.Background(), []Value{String(d[Text], "day"), source, String(d[Text], tc.token)}, InputEnvironment{Zone: zone, TimezoneAbbreviations: tc.set})
		want, _ := civilTimestamp(tc.y, tc.m, tc.day, tc.hour, tc.minute, 0, 0)
		if err != nil || v.integer != want {
			t.Fatal(tc, v, err, want)
		}
	}
}
