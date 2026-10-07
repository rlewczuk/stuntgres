package sqltype

import (
	"testing"
)

// REL_18_0 datetime.c deltatktbl and datetime.h unit macros define this
// vocabulary. DecodeUnits compares the first ten bytes of long tokens.
func TestTimezoneReviewAliasVocabulary(t *testing.T) {
	for _, tc := range []struct {
		unit    zoneIntervalUnit
		aliases []string
		want    string
	}{
		{zoneIntervalMillennium, []string{"mil", "mils", "millennia", "millennium", "millenniums"}, "1000 years"},
		{zoneIntervalCentury, []string{"c", "cent", "century", "centuries"}, "100 years"},
		{zoneIntervalDecade, []string{"dec", "decs", "decade", "decades"}, "10 years"},
		{zoneIntervalYear, []string{"y", "yr", "yrs", "year", "years"}, "1 year"},
		{zoneIntervalMonth, []string{"mon", "mons", "month", "months"}, "1 mon"},
		{zoneIntervalWeek, []string{"w", "week", "weeks"}, "7 days"},
		{zoneIntervalDay, []string{"d", "day", "days"}, "1 day"},
		{zoneIntervalHour, []string{"h", "hr", "hrs", "hour", "hours", "HRS"}, "01:00:00"},
		{zoneIntervalMinute, []string{"m", "min", "mins", "minute", "minutes"}, "00:01:00"},
		{zoneIntervalSecond, []string{"s", "sec", "secs", "second", "seconds"}, "00:00:01"},
		{zoneIntervalMillisecond, []string{"ms", "msec", "msecs", "msecond", "mseconds", "millisecon", "millisecond", "milliseconds", "millisecondsuffix"}, "00:00:00.001"},
		{zoneIntervalMicrosecond, []string{"us", "usec", "usecs", "usecond", "useconds", "microsecon", "microsecond", "microseconds", "microsecondsuffix"}, "00:00:00.000001"},
	} {
		for _, alias := range tc.aliases {
			t.Run(alias, func(t *testing.T) {
				if got := decodeZoneIntervalUnit(alias); got != tc.unit {
					t.Fatalf("unit %q = %v, want %v", alias, got, tc.unit)
				}
				for _, separator := range []string{"", " "} {
					got, err := DecodeZoneInterval("1"+separator+alias, "", -1)
					if err != nil || got.String() != tc.want {
						t.Fatalf("alias %q: %v, %v; want %v", alias, got, err, tc.want)
					}
				}
			})
		}
	}
	for _, input := range []string{"1 hrr", "1 msecx", "1 usecx", "1 microseco", "1 millise co", "1 hr 2 hours", "@", "@ ago", "9223372036854775808 useconds"} {
		if _, err := DecodeZoneInterval(input, "", -1); err == nil {
			t.Fatalf("accepted invalid interval %q", input)
		}
	}
}
