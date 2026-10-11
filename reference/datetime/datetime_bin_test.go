package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

// Independently specified release-source/manual expectations passed Main's
// pinned PostgreSQL 18.0 discovery in tests-1. No native output is a golden.
func datetimeBinScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			for _, q := range []string{"SET TIME ZONE 'UTC'", "SET DateStyle TO 'ISO, MDY'", "SET IntervalStyle TO 'postgres'"} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
			}
			body(t, p, b, c)
		})
	}
}

func datetimeBinCall(stride, typ, source, origin string) string {
	return fmt.Sprintf("date_bin(INTERVAL '%s',%s '%s',%s '%s')", stride, typ, source, typ, origin)
}

func datetimeBinAlignment(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, typ := range []string{"timestamp", "timestamptz"} {
		for _, tc := range []struct{ stride, source, origin, want string }{
			{"15 minutes", "2000-01-01 00:02:29.999999", "2000-01-01 00:02:30", "1999-12-31T23:47:30Z"},
			{"15 minutes", "2000-01-01 00:02:30", "2000-01-01 00:02:30", "2000-01-01T00:02:30Z"},
			{"15 minutes", "2000-01-01 00:17:29.999999", "2000-01-01 00:02:30", "2000-01-01T00:02:30Z"},
			{"15 minutes", "2000-01-01 00:17:30", "2000-01-01 00:02:30", "2000-01-01T00:17:30Z"},
			{"15 minutes", "1999-12-31 23:17:30", "2000-01-01 00:02:30", "1999-12-31T23:17:30Z"},
			{"15 minutes", "1999-12-31 23:17:29.999999", "2000-01-01 00:02:30", "1999-12-31T23:02:30Z"},
			{"30 minutes", "2024-02-01 15:00", "2024-02-01 17:00", "2024-02-01T15:00:00Z"},
			{"15 minutes", "2020-02-11 15:44:17", "2001-01-01", "2020-02-11T15:30:00Z"},
			{"15 minutes", "2020-02-11 15:44:17", "2001-01-01 00:02:30", "2020-02-11T15:32:30Z"},
			{"5 minutes", "2020-02-01 01:01:01", "2020-02-01 00:02:30", "2020-02-01T00:57:30Z"},
			{"250 microseconds", "2000-01-01 00:00:00.000099", "2000-01-01 00:00:00.000100", "1999-12-31T23:59:59.99985Z"},
			{"250 microseconds", "2000-01-01 00:00:00.000100", "2000-01-01 00:00:00.000100", "2000-01-01T00:00:00.0001Z"},
			{"250 microseconds", "2000-01-01 00:00:00.000349", "2000-01-01 00:00:00.000100", "2000-01-01T00:00:00.0001Z"},
			{"250 microseconds", "2000-01-01 00:00:00.000350", "2000-01-01 00:00:00.000100", "2000-01-01T00:00:00.00035Z"},
			{"30 days", "2000-02-01", "2000-01-01", "2000-01-31T00:00:00Z"},
			{"365 days", "2001-01-01", "2000-01-01", "2000-12-31T00:00:00Z"},
			{"1 day -1 hour", "2000-01-02 00:00", "2000-01-01", "2000-01-01T23:00:00Z"},
		} {
			t.Run(typ+"/"+tc.stride+"/"+tc.source, func(t *testing.T) {
				resultType := "TIMESTAMP"
				if typ == "timestamptz" {
					resultType = "TIMESTAMPTZ"
				}
				datetimeTruncValue(t, p, b, c, datetimeBinCall(tc.stride, typ, tc.source, tc.origin), resultType, tc.want)
				datetimeTruncValue(t, p, b, c, "date_bin($1::interval,$2::"+typ+",$3::"+typ+")", resultType, tc.want, tc.stride, tc.source, tc.origin)
			})
		}
		for _, source := range []string{"294276-12-31 23:59:59.999999", "4713-01-01 BC"} {
			want := source
			if typ == "timestamptz" {
				want += "+00"
			}
			if source == "4713-01-01 BC" {
				want = "4713-01-01 00:00:00 BC"
				if typ == "timestamptz" {
					want = "4713-01-01 00:00:00+00 BC"
				}
			}
			datetimeTruncValue(t, p, b, c, datetimeBinCall("1 microsecond", typ, source, source)+"::text", "TEXT", want)
		}
		// A maximal finite time component is not interval infinity.
		datetimeTruncValue(t, p, b, c, datetimeBinCall("2562047788:00:54.775807", typ, "2000-01-02", "2000-01-01")+"::text", "TEXT", map[string]string{"timestamp": "2000-01-01 00:00:00", "timestamptz": "2000-01-01 00:00:00+00"}[typ])
	}
	for _, zone := range []string{"UTC", "America/New_York"} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET TIME ZONE '"+zone+"'", 0))
		for _, stride := range []string{"1 day", "24 hours"} {
			datetimeTruncValue(t, p, b, c, datetimeBinCall(stride, "timestamptz", "2024-03-11 00:30-04", "2024-03-09 00:00-05"), "TIMESTAMPTZ", "2024-03-10T05:00:00Z")
		}
	}
}

func TestReferenceDatetimeBinAlignment(t *testing.T) {
	datetimeBinScenario(t, datetimeBinAlignment)
}
