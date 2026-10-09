package reference_test

import (
	"strings"
	"testing"
)

// Outcomes are independent release-source expectations, not native-derived
// offsets. The fixture pins PostgreSQL 18.0 UTF8/C.
func TestReferenceTimezoneReviewAliasSettings(t *testing.T) {
	p := timezoneReferencePair(t)
	p.step(t, referenceExec("SET IntervalStyle='postgres'", 0))
	for _, tc := range []struct{ number, aliases string }{
		{"1", "h hr hrs hour hours HRS"},
		{"60", "m min mins minute minutes"},
		{"3600", "s sec secs second seconds"},
		{"3600000", "ms msec msecs msecond mseconds millisecon millisecond milliseconds millisecondsuffix"},
		{"3600000000", "us usec usecs usecond useconds microsecon microsecond microseconds microsecondsuffix"},
	} {
		for _, alias := range strings.Fields(tc.aliases) {
			for _, separator := range []string{"", " "} {
				input := tc.number + separator + alias
				t.Run(input, func(t *testing.T) {
					for _, prepared := range []bool{false, true} {
						for _, q := range []string{"SET TIME ZONE INTERVAL '" + input + "'", "SET timezone='INTERVAL ''" + input + "'''", "SELECT set_config('timezone','INTERVAL ''" + input + "''',false)"} {
							timezoneBoth(t, p, referenceExec("SET TIME ZONE 'America/New_York'", 0))
							s := referenceExec(q, 0)
							if strings.HasPrefix(q, "SELECT") {
								s = referenceSelect(q, []string{"set_config"}, []string{"TEXT"}, [][]any{{"<+01>-01"}})
							}
							s.prepared = prepared
							p.step(t, s)
							show := referenceSelect("SHOW TimeZone", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{"<+01>-01"}})
							show.prepared = prepared
							p.step(t, show)
							conversion := referenceSelect("SELECT ('2004-07-01 12:00'::TEXT::TIMESTAMPTZ AT TIME ZONE 'UTC')::TEXT AS witness FROM tz_witness", []string{"witness"}, []string{"TEXT"}, [][]any{{"2004-07-01 11:00:00"}})
							conversion.prepared = prepared
							p.step(t, conversion)
						}
					}
				})
			}
		}
	}
	for _, tc := range []struct{ input, normalized, detail string }{
		{"1 c", "100 years", "Cannot specify months in time zone interval."},
		{"1 cent", "100 years", "Cannot specify months in time zone interval."},
		{"1 dec", "10 years", "Cannot specify months in time zone interval."},
		{"1 decs", "10 years", "Cannot specify months in time zone interval."},
		{"1 mil", "1000 years", "Cannot specify months in time zone interval."},
		{"1 mils", "1000 years", "Cannot specify months in time zone interval."},
		{"1 yr", "1 year", "Cannot specify months in time zone interval."},
		{"1 yrs", "1 year", "Cannot specify months in time zone interval."},
		{"1 mons", "1 mon", "Cannot specify months in time zone interval."},
		{"1 w", "7 days", "Cannot specify days in time zone interval."},
		{"1 d", "1 day", "Cannot specify days in time zone interval."},
		{"1 century", "100 years", "Cannot specify months in time zone interval."},
		{"1 centuries", "100 years", "Cannot specify months in time zone interval."},
		{"1 decade", "10 years", "Cannot specify months in time zone interval."},
		{"1 decades", "10 years", "Cannot specify months in time zone interval."},
		{"1 millennia", "1000 years", "Cannot specify months in time zone interval."},
		{"1 millennium", "1000 years", "Cannot specify months in time zone interval."},
		{"1 millenniums", "1000 years", "Cannot specify months in time zone interval."},
		{"1 y", "1 year", "Cannot specify months in time zone interval."},
		{"1 year", "1 year", "Cannot specify months in time zone interval."},
		{"1 years", "1 year", "Cannot specify months in time zone interval."},
		{"1 mon", "1 mon", "Cannot specify months in time zone interval."},
		{"1 month", "1 mon", "Cannot specify months in time zone interval."},
		{"1 months", "1 mon", "Cannot specify months in time zone interval."},
		{"1 week", "7 days", "Cannot specify days in time zone interval."},
		{"1 weeks", "7 days", "Cannot specify days in time zone interval."},
		{"1 day", "1 day", "Cannot specify days in time zone interval."},
		{"1 days", "1 day", "Cannot specify days in time zone interval."},
		{"1 hr 1 day 1 mon", "1 mon 1 day 01:00:00", "Cannot specify months in time zone interval."},
		{"168 hrs", "168:00:00", "UTC timezone offset is out of range."},
		{"1 hrr", "", ""}, {"1 msecx", "", ""}, {"1 usecx", "", ""}, {"1 hr 2 hours", "", ""}, {"@ ago", "", ""}, {"9223372036854775808 useconds", "", ""},
	} {
		for _, prepared := range []bool{false, true} {
			for _, q := range []string{"SET TIME ZONE INTERVAL '" + tc.input + "'", "SET timezone='INTERVAL ''" + tc.input + "'''", "SELECT set_config('timezone','INTERVAL ''" + tc.input + "''',false)"} {
				timezoneBoth(t, p, referenceExec("SET TIME ZONE 'America/New_York'", 0))
				want := referenceDiagnostic{Code: "22007", Message: "invalid input syntax for type interval: \"" + tc.input + "\""}
				if strings.HasPrefix(tc.input, "922337") {
					want.Code, want.Message = "22015", "interval field value out of range: \""+tc.input+"\""
				} else if tc.detail != "" {
					value := tc.input
					if strings.HasPrefix(q, "SET TIME ZONE") {
						value = tc.normalized
					}
					want = referenceDiagnostic{Code: "22023", Message: "invalid value for parameter \"TimeZone\": \"INTERVAL '" + value + "'\"", Detail: tc.detail}
				}
				s := referenceImplicitError(q, want)
				s.prepared, s.selectRows, s.errorStage = prepared, strings.HasPrefix(q, "SELECT"), "execute"
				p.step(t, s)
				timezoneBoth(t, p, referenceSelect("SHOW TimeZone", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{"America/New_York"}}))
				timezoneBoth(t, p, referenceSelect("SELECT ('2004-07-01 12:00'::TEXT::TIMESTAMPTZ AT TIME ZONE 'UTC')::TEXT AS witness FROM tz_witness", []string{"witness"}, []string{"TEXT"}, [][]any{{"2004-07-01 16:00:00"}}))
			}
		}
	}
}
