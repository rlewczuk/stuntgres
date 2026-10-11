package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDatetimeTruncConfiguredAbbreviations(t *testing.T) {
	datetimeTruncScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		// REL_18_0 DecodeTimezoneName -> DecodeTimezoneAbbrev checks the session
		// zone before the configured table; truncation then uses a dynamic
		// abbreviation's full owning zone, not a forced abbreviation offset.
		defer func() {
			if _, err := c.ExecContext(p.Ctx, "SET timezone_abbreviations='Default'"); err != nil {
				t.Error(err)
			}
		}()
		q := "SELECT date_trunc('day',$1::timestamptz,$2::text) AT TIME ZONE 'UTC' AS result"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct{ set, session, est, cst string }{
			{"Default", "UTC", "2004-07-01T05:00:00Z", "2004-07-01T06:00:00Z"},
			{"Default", "America/New_York", "2004-07-01T05:00:00Z", "2004-07-01T06:00:00Z"},
			{"Australia", "UTC", "2004-06-30T14:00:00Z", "2004-06-30T14:30:00Z"},
			{"Australia", "America/New_York", "2004-07-01T05:00:00Z", "2004-06-30T14:30:00Z"},
			{"Default", "America/New_York", "2004-07-01T05:00:00Z", "2004-07-01T06:00:00Z"},
		} {
			t.Run(tc.set+"/"+tc.session, func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET TIME ZONE '"+tc.session+"'", 0))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET timezone_abbreviations='"+tc.set+"'", 0))
				for _, zone := range []struct{ name, want string }{{"EST", tc.est}, {"CST", tc.cst}} {
					t.Run(zone.name, func(t *testing.T) {
						s := fixture.Select(q, []string{"result"}, []string{"TIMESTAMP"}, [][]any{{datetimeTruncInstant(zone.want)}}, "2004-07-01 12:00+00", zone.name)
						datetimeExtractBoth(t, p, b, c, s)
						t.Run("held", func(t *testing.T) { fixture.RunStep(t, p.Ctx, held, b.Recorder, s) })
					})
				}
			})
		}
	})
}
