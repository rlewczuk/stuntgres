package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceDatetimeMakeFormsMetadata(t *testing.T) {
	datetimeMakeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		date := any("2013-07-15")
		clock := any("08:15:23.5")
		if b.PG {
			date = time.Date(2013, 7, 15, 0, 0, 0, 0, time.UTC)
			clock = time.Date(0, 1, 1, 8, 15, 23, 500000000, time.UTC)
		}
		ts := time.Date(2013, 7, 15, 8, 15, 23, 500000000, time.UTC)
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT make_date(2013,7,15),make_time(8,15,23.5),make_timestamp(2013,7,15,8,15,23.5),make_timestamptz(2013,7,15,8,15,23.5,'UTC'),make_interval(1,2,3,4,5,6,7)", []string{"make_date", "make_time", "make_timestamp", "make_timestamptz", "make_interval"}, []string{"DATE", "TIME", "TIMESTAMP", "TIMESTAMPTZ", "INTERVAL"}, [][]any{{date, clock, ts, ts, "1 year 2 mons 25 days 05:06:07"}})
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			for _, predicate := range []string{"", " WHERE false"} {
				rows := [][]any{{nil, nil, nil, nil, nil}}
				if predicate != "" {
					rows = [][]any{}
				}
				s = fixture.Select("SELECT make_date(NULL,7,15),make_time(8,NULL,23),make_timestamp(2013,7,NULL,8,15,23),make_timestamptz(2013,7,15,8,15,23,NULL),make_interval(secs=>NULL)"+predicate, []string{"make_date", "make_time", "make_timestamp", "make_timestamptz", "make_interval"}, []string{"DATE", "TIME", "TIMESTAMP", "TIMESTAMPTZ", "INTERVAL"}, rows)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
	})
}

func TestReferenceDatetimeMakeCalendarBounds(t *testing.T) {
	datetimeMakeTextCases(t, []datetimeMakeCase{
		{"AD", "make_date(1,1,1)", "0001-01-01"}, {"BC1", "make_date(-1,1,1)", "0001-01-01 BC"}, {"BC44", "make_date(-44,3,15)", "0044-03-15 BC"},
		{"BCLeap", "make_date(-1,2,29)", "0001-02-29 BC"}, {"leap", "make_date(2000,2,29)", "2000-02-29"},
		{"dateMin", "make_date(-4714,11,24)", "4714-11-24 BC"}, {"dateMax", "make_date(5874897,12,31)", "5874897-12-31"},
		{"wideDate", "make_date(300000,1,1)", "300000-01-01"},
		{"timestampMin", "make_timestamp(-4714,11,24,0,0,0)", "4714-11-24 00:00:00 BC"},
		{"timestampLast", "make_timestamp(294276,12,31,23,59,59.999999)", "294276-12-31 23:59:59.999999"},
		{"BCtimestamp", "make_timestamp(-44,3,15,12,0,0)", "0044-03-15 12:00:00 BC"},
	})
}

func TestReferenceDatetimeMakeClockSeconds(t *testing.T) {
	datetimeMakeTextCases(t, []datetimeMakeCase{
		{"endpoint", "make_time(24,0,0)", "24:00:00"}, {"carryDate", "make_timestamp(1999,12,31,24,0,0)", "2000-01-01 00:00:00"},
		{"sec60", "make_time(12,34,60)", "12:35:00"}, {"evenTie", "make_time(0,0,0.0000005)", "00:00:00"}, {"oddTie", "make_time(0,0,0.0000015)", "00:00:00.000002"},
		{"carryRound", "make_time(23,59,59.9999995)", "24:00:00"}, {"tinyNegative", "make_time(0,0,-0.0000004)", "00:00:00"},
		{"tinyOver60", "make_time(12,34,60.0000004)", "12:35:00"}, {"endpointTiny", "make_time(24,0,0.0000004)", "24:00:00"},
		{"secondsNumeric", "make_time(8::smallint,15::smallint,23.5::numeric)", "08:15:23.5"},
		{"secondsReal", "make_time(8,15,23.5::real)", "08:15:23.5"},
	})
}

func TestReferenceDatetimeMakeIntervalOverflow(t *testing.T) {
	datetimeMakeTextCases(t, []datetimeMakeCase{
		{"zero", "make_interval()", "00:00:00"}, {"year", "make_interval(1)", "1 year"},
		{"mixed", "make_interval(1,-1,5,-7,25,-180,0)", "11 mons 28 days 22:00:00"},
		{"unjustifiedHours", "make_interval(hours=>24)", "24:00:00"}, {"unjustifiedDays", "make_interval(days=>30)", "30 days"},
		{"evenTie", "make_interval(secs=>0.0000005)", "00:00:00"}, {"oddTie", "make_interval(secs=>0.0000015)", "00:00:00.000002"},
		{"negativeTie", "make_interval(secs=>-0.0000015)", "-00:00:00.000002"},
		{"yearsBoundary", "make_interval(years=>178956970)", "178956970 years"},
		{"weeksBoundary", "make_interval(weeks=>306783378)", "2147483646 days"},
	})
	datetimeMakeFailures(t, []datetimeMakeFailure{
		{name: "yearsMultiply", expression: "make_interval(years=>178956971,months=>-12)", code: "22008", message: "interval out of range"},
		{name: "weeksMultiply", expression: "make_interval(weeks=>306783379,days=>-7)", code: "22008", message: "interval out of range"},
		{name: "monthsAdd", expression: "make_interval(years=>1,months=>2147483647)", code: "22008", message: "interval out of range"},
		{name: "daysAdd", expression: "make_interval(weeks=>1,days=>2147483647)", code: "22008", message: "interval out of range"},
		{name: "positiveMicros", expression: "make_interval(secs=>9223372036854.775808)", code: "22008", message: "interval out of range"},
		{name: "finalAdd", expression: "make_interval(hours=>2147483647,secs=>9223372036854)", code: "22008", message: "interval out of range"},
		{name: "nan", expression: "make_interval(secs=>'NaN'::float8)", code: "22008", message: "interval out of range"},
		{name: "infinity", expression: "make_interval(secs=>'Infinity'::float8)", code: "22008", message: "interval out of range"},
		{name: "negativeInfinity", expression: "make_interval(secs=>'-Infinity'::float8)", code: "22008", message: "interval out of range"},
		{name: "floatMultiply", expression: "make_interval(secs=>1e308)", code: "22003", message: "value out of range: overflow"},
	})
}
