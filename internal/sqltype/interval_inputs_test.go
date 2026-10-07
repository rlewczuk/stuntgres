package sqltype

import (
	"context"
	"testing"
)

func TestIntervalAliasesAndFractionBranches(t *testing.T) {
	d := testTypes()[Interval]
	ctx := context.Background()
	for _, tc := range []struct {
		aliases []string
		out     string
	}{{[]string{"y", "year", "years", "yr", "yrs"}, "1 year"}, {[]string{"mon", "mons", "month", "months"}, "1 mon"}, {[]string{"d", "day", "days"}, "1 day"}, {[]string{"h", "hour", "hours", "hr", "hrs"}, "01:00:00"}, {[]string{"m", "min", "mins", "minute", "minutes"}, "00:01:00"}, {[]string{"s", "sec", "secs", "second", "seconds"}, "00:00:01"}, {[]string{"ms", "msec", "msecs", "mseconds", "millisecond", "milliseconds"}, "00:00:00.001"}, {[]string{"us", "usec", "usecs", "useconds", "microsecond", "microseconds"}, "00:00:00.000001"}, {[]string{"w", "week", "weeks"}, "7 days"}, {[]string{"dec", "decs", "decade", "decades"}, "10 years"}, {[]string{"c", "cent", "century", "centuries"}, "100 years"}, {[]string{"mil", "mils", "millennia", "millennium"}, "1000 years"}} {
		for _, alias := range tc.aliases {
			v, e := d.Input(ctx, "1 "+alias, -1)
			if e != nil {
				t.Fatal(alias, e)
			}
			out, e := d.Output(ctx, v)
			if e != nil || out != tc.out {
				t.Fatal(alias, out, e)
			}
		}
	}
	for _, tc := range []struct{ s, out string }{{"0.125 years", "2 mons"}, {"0.375 years", "4 mons"}, {"-1.75 months", "-1 mons -22 days -12:00:00"}, {"-1.5 weeks", "-10 days -12:00:00"}, {"@ 1 day ago", "-1 days"}, {"1day", "1 day"}, {"- 1 day", "-1 days"}, {"00:00:60", "00:01:00"}, {"PT1e2S", "00:01:40"}, {"PT10", "10:00:00"}, {"P2-10-15", "2 years 10 mons 15 days"}, {"4:00 1.75 months", "1 mon 22 days 04:00:00"}, {"1.75 months 4:00", "1 mon 22 days 16:00:00"}} {
		v, e := d.Input(ctx, tc.s, -1)
		if e != nil {
			t.Fatal(tc.s, e)
		}
		out, e := d.Output(ctx, v)
		if e != nil || out != tc.out {
			t.Fatal(tc.s, out, tc.out, e)
		}
	}
}
