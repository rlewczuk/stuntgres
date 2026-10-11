package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDatetimeExtractPrecisionEpoch(t *testing.T) {
	datetimeExtractScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			field, source, numeric string
			floating               float64
		}{
			{"second", "TIME '17:12:28.5'", "28.500000", 28.5},
			{"milliseconds", "TIME '17:12:28.5'", "28500.000", 28500},
			{"microseconds", "TIME '17:12:28.5'", "28500000", 28500000},
			{"julian", "DATE '2006-01-01'", "2453737", 2453737},
			{"julian", "TIMESTAMP '2006-01-01 12:00'", "2453737.50000000000000000000", 2453737.5},
			{"epoch", "TIMESTAMP '2001-02-16 20:38:40.12'", "982355920.120000", 982355920.12},
			{"epoch", "TIMESTAMPTZ '2001-02-16 20:38:40.12-08'", "982384720.120000", 982384720.12},
			{"epoch", "TIMESTAMP '1969-12-31 23:59:59.999999'", "-0.000001", -0.000001},
			{"epoch", "DATE '1970-01-02'", "86400", 86400},
			{"hour", "TIME '24:00'", "24", 24}, {"epoch", "TIME '24:00'", "86400.000000", 86400},
			{"epoch", "TIMETZ '00:01+05:45'", "-20640.000000", -20640},
			{"timezone", "TIMETZ '00:01+05:45'", "20700", 20700},
			{"timezone_hour", "TIMETZ '00:01+05:45'", "5", 5},
			{"timezone_minute", "TIMETZ '00:01+05:45'", "45", 45},
		} {
			t.Run(tc.field+"/"+tc.source, func(t *testing.T) { datetimeExtractPair(t, p, b, c, tc.field, tc.source, tc.numeric, tc.floating) })
		}
		for _, source := range []string{"TIME '17:12:28.123456'", "TIMETZ '17:12:28.123456-05:30'", "TIMESTAMP '2000-01-01 17:12:28.123456'", "TIMESTAMPTZ '2000-01-01 17:12:28.123456+00'", "INTERVAL '17 hours 12 minutes 28.123456 seconds'"} {
			for _, tc := range []struct {
				field, numeric string
				floating       float64
			}{{"second", "28.123456", 28.123456}, {"milliseconds", "28123.456", 28123.456}, {"microseconds", "28123456", 28123456}} {
				t.Run(source+"/"+tc.field, func(t *testing.T) { datetimeExtractPair(t, p, b, c, tc.field, source, tc.numeric, tc.floating) })
			}
		}
	})
}

func TestReferenceDatetimeExtractIntervalComponents(t *testing.T) {
	datetimeExtractScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			field, interval, numeric string
			floating                 float64
		}{
			{"year", "2 years 13 months", "3", 3}, {"month", "2 years 13 months", "1", 1},
			{"year", "-2 years -13 months", "-3", -3}, {"month", "-2 years -13 months", "-1", -1},
			{"hour", "80 minutes", "1", 1}, {"minute", "80 minutes", "20", 20},
			{"hour", "-80 minutes", "-1", -1}, {"minute", "-80 minutes", "-20", -20},
			{"week", "13 days 24 hours", "1", 1}, {"day", "13 days 24 hours", "13", 13}, {"hour", "13 days 24 hours", "24", 24},
			{"week", "-13 days -24 hours", "-1", -1}, {"day", "-13 days -24 hours", "-13", -13}, {"hour", "-13 days -24 hours", "-24", -24},
			{"year", "-13 months 3 days -80 minutes", "-1", -1}, {"month", "-13 months 3 days -80 minutes", "-1", -1},
			{"day", "-13 months 3 days -80 minutes", "3", 3}, {"hour", "-13 months 3 days -80 minutes", "-1", -1}, {"minute", "-13 months 3 days -80 minutes", "-20", -20},
			{"quarter", "1 year 6 months", "3", 3}, {"quarter", "-1 year -6 months", "-3", -3},
			{"quarter", "0", "1", 1}, {"quarter", "-1 year", "-1", -1},
			{"epoch", "1 year", "31557600.000000", 31557600}, {"epoch", "1 month", "2592000.000000", 2592000},
			{"epoch", "5 days 3 hours", "442800.000000", 442800}, {"week", "-13 days", "-1", -1},
			{"epoch", "-1 year 1 month -1 day 1 microsecond", "-28598399.999999", -28598399.999999},
			// More than 2^53 integral microseconds: no numeric float intermediate.
			{"epoch", "1000000000 days 1 microsecond", "86400000000000.000001", 86400000000000},
		} {
			t.Run(tc.field+"/"+tc.interval, func(t *testing.T) {
				datetimeExtractPair(t, p, b, c, tc.field, "INTERVAL '"+tc.interval+"'", tc.numeric, tc.floating)
			})
		}
	})
}
