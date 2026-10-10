package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Independently specified expectations were validated against Main's pinned
// PostgreSQL 18.0 oracle in tests-2; both backends execute the same bodies.
func datetimeTruncScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend, *sql.Conn)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			c, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			for _, q := range []string{"SET TIME ZONE 'UTC'", "SET DateStyle TO 'ISO, MDY'", "SET IntervalStyle TO 'postgres'"} {
				referenceRunStep(t, p.ctx, c, b.recorder, referenceExec(q, 0))
			}
			body(t, p, b, c)
		})
	}
}

func datetimeTruncInstant(s string) time.Time {
	v, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		panic(err)
	}
	return v
}

func datetimeTruncValue(t *testing.T, p *referencePair, b referenceBackend, c referenceExecutor, expression, typ string, want any, args ...any) {
	t.Helper()
	if s, ok := want.(string); ok && (typ == "TIMESTAMP" || typ == "TIMESTAMPTZ") {
		want = datetimeTruncInstant(s)
	}
	datetimeExtractBoth(t, p, b, c, referenceSelect("SELECT "+expression+" AS result", []string{"result"}, []string{typ}, [][]any{{want}}, args...))
}

func TestReferenceDatetimeTruncFieldsCalendar(t *testing.T) {
	datetimeTruncScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, tc := range []struct{ aliases, want string }{
			{"microseconds microsecond us usec usecond useconds usecs MICROSECONDS microseconds_extra", "2001-02-16 20:38:40.123456"},
			{"milliseconds millisecond ms msec msecond mseconds msecs milliseconds_extra", "2001-02-16 20:38:40.123"},
			{"second seconds s sec secs", "2001-02-16 20:38:40"}, {"minute minutes m min mins", "2001-02-16 20:38:00"},
			{"hour hours h hr hrs", "2001-02-16 20:00:00"}, {"day days d", "2001-02-16 00:00:00"},
			{"week weeks w", "2001-02-12 00:00:00"}, {"month months mon mons", "2001-02-01 00:00:00"},
			{"quarter qtr", "2001-01-01 00:00:00"}, {"year years y yr yrs", "2001-01-01 00:00:00"},
			{"decade decades dec decs", "2000-01-01 00:00:00"}, {"century centuries c cent", "2001-01-01 00:00:00"},
			{"millennium millennia millenniums mil mils", "2001-01-01 00:00:00"},
		} {
			for _, field := range strings.Fields(tc.aliases) {
				t.Run(field, func(t *testing.T) {
					datetimeTruncValue(t, p, b, c, "date_trunc('"+field+"',TIMESTAMP '2001-02-16 20:38:40.123456')::text", "TEXT", tc.want)
					datetimeTruncValue(t, p, b, c, "date_trunc($1::text,$2::timestamp)::text", "TEXT", tc.want, field, "2001-02-16 20:38:40.123456")
				})
			}
		}
		for _, tc := range []struct{ field, source, want string }{
			{"week", "2006-01-01", "2005-12-26 00:00:00"}, {"week", "2012-12-31", "2012-12-31 00:00:00"},
			{"week", "2004-02-29", "2004-02-23 00:00:00"}, {"milliseconds", "1999-12-31 23:59:59.999999", "1999-12-31 23:59:59.999"},
			{"decade", "0001-01-01", "0001-01-01 00:00:00 BC"}, {"century", "0001-01-01 BC", "0100-01-01 00:00:00 BC"},
			{"millennium", "0001-01-01 BC", "1000-01-01 00:00:00 BC"}, {"century", "2000-12-31", "1901-01-01 00:00:00"},
			{"millennium", "2000-12-31", "1001-01-01 00:00:00"}, {"decade", "0011-06-01 BC", "0011-01-01 00:00:00 BC"},
		} {
			t.Run(tc.field+tc.source, func(t *testing.T) {
				datetimeTruncValue(t, p, b, c, fmt.Sprintf("date_trunc('%s',TIMESTAMP '%s')::text", tc.field, tc.source), "TEXT", tc.want)
			})
		}
		for _, typ := range []string{"timestamp", "timestamptz", "interval"} {
			for _, sign := range []string{"infinity", "-infinity"} {
				for _, field := range strings.Fields("microseconds milliseconds second minute hour day month quarter year decade century millennium") {
					datetimeTruncValue(t, p, b, c, fmt.Sprintf("date_trunc('%s','%s'::%s)::text", field, sign, typ), "TEXT", sign)
				}
			}
		}
	})
}

func TestReferenceDatetimeTruncIntervals(t *testing.T) {
	datetimeTruncScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, tc := range []struct{ field, source, want string }{
			{"microseconds", "1 year 2 mons 3 days 04:05:06.789123", "1 year 2 mons 3 days 04:05:06.789123"},
			{"milliseconds", "1 year 2 mons 3 days 04:05:06.789123", "1 year 2 mons 3 days 04:05:06.789"},
			{"second", "1 year 2 mons 3 days 04:05:06.789123", "1 year 2 mons 3 days 04:05:06"},
			{"minute", "1 year 2 mons 3 days 04:05:06.789123", "1 year 2 mons 3 days 04:05:00"},
			{"hour", "1 year 2 mons 3 days 04:05:06.789123", "1 year 2 mons 3 days 04:00:00"},
			{"day", "1 year 2 mons 3 days 04:05:06.789123", "1 year 2 mons 3 days"},
			{"month", "1 year 2 mons 3 days 04:05:06.789123", "1 year 2 mons"},
			{"quarter", "1 year 2 mons 3 days 04:05:06.789123", "1 year"}, {"year", "1 year 2 mons 3 days 04:05:06.789123", "1 year"},
			{"decade", "19 years 11 mons 3 days", "10 years"}, {"century", "199 years 11 mons", "100 years"}, {"millennium", "1999 years 11 mons", "1000 years"},
			{"quarter", "-1 year -4 mons", "-1 years -3 mons"}, {"second", "-00:00:01.999999", "-00:00:01"},
			{"milliseconds", "-00:00:01.999999", "-00:00:01.999"}, {"hour", "49 hours 5 minutes", "49:00:00"}, {"day", "49 hours 5 minutes", "00:00:00"},
			{"hour", "1 mon -2 days -03:04:05", "1 mon -2 days -03:00:00"},
		} {
			t.Run(tc.field+tc.source, func(t *testing.T) {
				datetimeTruncValue(t, p, b, c, "date_trunc($1::text,$2::interval)::text", "TEXT", tc.want, tc.field, tc.source)
			})
		}
		for _, style := range []struct{ name, want string }{{"postgres", "1 year 2 mons 3 days"}, {"postgres_verbose", "@ 1 year 2 mons 3 days"}, {"sql_standard", "+1-2 +3 +0:00:00"}, {"iso_8601", "P1Y2M3D"}} {
			referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("SET IntervalStyle TO '"+style.name+"'", 0))
			datetimeTruncValue(t, p, b, c, "date_trunc('day',INTERVAL '1 year 2 mons 3 days 04:05:06')::text", "TEXT", style.want)
		}
	})
}
