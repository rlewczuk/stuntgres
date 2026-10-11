package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func datetimeTruncCalendarZones(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	q2 := "SELECT date_trunc($1::text,$2::timestamptz) AT TIME ZONE 'UTC' AS result"
	q3 := "SELECT date_trunc($1::text,$2::timestamptz,$3::text) AT TIME ZONE 'UTC' AS result"
	s2, err := c.PrepareContext(p.Ctx, q2)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	s3, err := c.PrepareContext(p.Ctx, q3)
	if err != nil {
		t.Fatal(err)
	}
	defer s3.Close()
	x2 := fixture.HeldStatement{Executor: c, Statement: s2, SQL: q2}
	x3 := fixture.HeldStatement{Executor: c, Statement: s3, SQL: q3}
	for _, tc := range []struct{ field, source, ny, utc string }{
		{"week", "2024-03-10 12:00+00", "2024-03-04T05:00:00Z", "2024-03-04T00:00:00Z"},
		{"month", "2024-03-10 12:00+00", "2024-03-01T05:00:00Z", "2024-03-01T00:00:00Z"},
		{"quarter", "2024-03-10 12:00+00", "2024-01-01T05:00:00Z", "2024-01-01T00:00:00Z"},
		{"year", "2024-03-10 12:00+00", "2024-01-01T05:00:00Z", "2024-01-01T00:00:00Z"},
		{"week", "2024-11-03 12:00+00", "2024-10-28T04:00:00Z", "2024-10-28T00:00:00Z"},
		{"month", "2024-11-03 12:00+00", "2024-11-01T04:00:00Z", "2024-11-01T00:00:00Z"},
		{"quarter", "2024-11-03 12:00+00", "2024-10-01T04:00:00Z", "2024-10-01T00:00:00Z"},
		{"year", "2024-11-03 12:00+00", "2024-01-01T05:00:00Z", "2024-01-01T00:00:00Z"},
	} {
		t.Run(tc.field+tc.source, func(t *testing.T) {
			for _, session := range []string{"America/New_York", "UTC", "America/New_York"} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET TIME ZONE '"+session+"'", 0))
				want := tc.ny
				if session == "UTC" {
					want = tc.utc
				}
				a := fixture.Select(q2, []string{"result"}, []string{"TIMESTAMP"}, [][]any{{datetimeTruncInstant(want)}}, tc.field, tc.source)
				datetimeExtractBoth(t, p, b, c, a)
				fixture.RunStep(t, p.Ctx, x2, b.Recorder, a)
				z := fixture.Select(q3, []string{"result"}, []string{"TIMESTAMP"}, [][]any{{datetimeTruncInstant(tc.ny)}}, tc.field, tc.source, "America/New_York")
				datetimeExtractBoth(t, p, b, c, z)
				fixture.RunStep(t, p.Ctx, x3, b.Recorder, z)
			}
		})
	}
}
func TestReferenceDatetimeTruncCalendarZonesDST(t *testing.T) {
	datetimeTruncScenario(t, datetimeTruncCalendarZones)
}
