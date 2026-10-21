package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func timestamptzSeriesScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET TIME ZONE 'UTC'", 0))
			body(t, p, b, c)
		})
	}
}

const timestamptzSeriesNY = "generate_series(TIMESTAMPTZ '2024-03-09 12:00-05',TIMESTAMPTZ '2024-03-11 13:00-04',INTERVAL '1 day','America/New_York')"

func timestamptzSeriesSpring() [][]any {
	return timestampSeriesRows("2024-03-09 17:00:00", "2024-03-10 16:00:00", "2024-03-11 16:00:00")
}

func timestamptzSeriesUTC() [][]any {
	return timestampSeriesRows("2024-03-09 17:00:00", "2024-03-10 17:00:00", "2024-03-11 17:00:00")
}

func TestReferenceTimestamptzSeriesValues(t *testing.T) {
	timestamptzSeriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			start, stop, step, suffix string
			values                    []string
		}{
			{"2024-03-09 12:00-05", "2024-03-11 13:00-04", "1 day", " ORDER BY v", []string{"2024-03-09 17:00:00", "2024-03-10 16:00:00", "2024-03-11 16:00:00"}},
			{"2024-03-09 12:00-05", "2024-03-11 13:00-04", "24 hours", " ORDER BY v", []string{"2024-03-09 17:00:00", "2024-03-10 17:00:00", "2024-03-11 17:00:00"}},
			{"2024-01-31 12:00-05", "2024-03-31 12:00-04", "1 month", " ORDER BY v", []string{"2024-01-31 17:00:00", "2024-02-29 17:00:00", "2024-03-29 16:00:00"}},
			{"2024-03-09 02:30-05", "2024-03-11 04:00-04", "1 day", " ORDER BY v", []string{"2024-03-09 07:30:00", "2024-03-10 07:30:00", "2024-03-11 07:30:00"}},
			{"2024-11-02 01:30-04", "2024-11-04 02:00-05", "1 day", " ORDER BY v", []string{"2024-11-02 05:30:00", "2024-11-03 06:30:00", "2024-11-04 06:30:00"}},
			{"2024-03-11 12:00-04", "2024-03-09 12:00-05", "-1 day", " ORDER BY v DESC", []string{"2024-03-11 16:00:00", "2024-03-10 16:00:00", "2024-03-09 17:00:00"}},
			{"2024-03-09 12:00-05", "2024-03-11 12:00-04", "1 day -23 hours", " LIMIT 3", []string{"2024-03-09 17:00:00", "2024-03-09 17:00:00", "2024-03-09 17:00:00"}},
			{"2024-01-30 12:00-05", "2024-03-01 13:00-05", "1 month 1 day 1 hour", " ORDER BY v", []string{"2024-01-30 17:00:00", "2024-03-01 18:00:00"}},
			{"2024-03-09 12:00:00.000001-05", "2024-03-09 12:00:00.000005-05", "2 microseconds", " ORDER BY v", []string{"2024-03-09 17:00:00.000001", "2024-03-09 17:00:00.000003", "2024-03-09 17:00:00.000005"}},
			{"2024-03-09 12:00-05", "2024-03-12 12:00-04", "2 days", " ORDER BY v", []string{"2024-03-09 17:00:00", "2024-03-11 16:00:00"}},
			{"2024-03-09 12:00-05", "2024-03-09 12:00-05", "1 day", "", []string{"2024-03-09 17:00:00"}},
			{"2024-03-09 12:00-05", "2024-03-09 12:00-05", "-1 day", "", []string{"2024-03-09 17:00:00"}},
			{"2024-03-11 12:00-04", "2024-03-09 12:00-05", "1 day", "", nil},
			{"2024-03-09 12:00-05", "2024-03-11 12:00-04", "-1 day", "", nil},
		} {
			t.Run(tc.start+"/"+tc.step, func(t *testing.T) {
				q := "SELECT generate_series(TIMESTAMPTZ '" + tc.start + "',TIMESTAMPTZ '" + tc.stop + "',INTERVAL '" + tc.step + "','America/New_York') AS v" + tc.suffix
				timestampSeriesBoth(t, p, b, c, fixture.Select(q, []string{"v"}, []string{"TIMESTAMPTZ"}, timestampSeriesRows(tc.values...)))
			})
		}
		q := "SELECT generate_series(TIMESTAMPTZ '2001-10-22 00:00-04',TIMESTAMPTZ '2001-11-01 00:00-05',INTERVAL '1 day','America/New_York') AS v ORDER BY v"
		timestampSeriesBoth(t, p, b, c, fixture.Select(q, []string{"v"}, []string{"TIMESTAMPTZ"}, timestampSeriesRows(
			"2001-10-22 04:00:00", "2001-10-23 04:00:00", "2001-10-24 04:00:00", "2001-10-25 04:00:00", "2001-10-26 04:00:00", "2001-10-27 04:00:00", "2001-10-28 04:00:00", "2001-10-29 05:00:00", "2001-10-30 05:00:00", "2001-10-31 05:00:00", "2001-11-01 05:00:00")))
	})
}

func TestReferenceTimestamptzSeriesZones(t *testing.T) {
	timestamptzSeriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			set, session, zone string
			dst                bool
		}{
			{"Default", "UTC", "America/New_York", true},
			{"Default", "UTC", "america/new_york", true},
			{"Default", "UTC", "US/Eastern", true},
			{"Default", "UTC", "EST5EDT", true},
			{"Default", "UTC", "EST", false},
			{"Default", "America/New_York", "EST", false},
			{"Australia", "UTC", "EST", false},
			{"Australia", "America/New_York", "EST", false},
			{"India", "UTC", "IST", false},
		} {
			t.Run(tc.set+"/"+tc.session+"/"+tc.zone, func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET timezone_abbreviations='"+tc.set+"'", 0))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET TIME ZONE '"+tc.session+"'", 0))
				want := timestamptzSeriesUTC()
				if tc.dst {
					want = timestamptzSeriesSpring()
				}
				q := "SELECT generate_series(TIMESTAMPTZ '2024-03-09 12:00-05',TIMESTAMPTZ '2024-03-11 13:00-04',INTERVAL '1 day',$1::text) AS v ORDER BY v"
				timestampSeriesBoth(t, p, b, c, fixture.Select(q, []string{"v"}, []string{"TIMESTAMPTZ"}, want, tc.zone))
			})
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET timezone_abbreviations='Default'", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET TIME ZONE 'UTC'", 0))
		// Stock dynamic LHDT denotes the full Lord_Howe owner, not forced +11.
		q := "SELECT generate_series(TIMESTAMPTZ '2024-04-06 12:00+11',TIMESTAMPTZ '2024-04-08 13:00+10:30',INTERVAL '1 day','LHDT') AS v ORDER BY v"
		timestampSeriesBoth(t, p, b, c, fixture.Select(q, []string{"v"}, []string{"TIMESTAMPTZ"}, timestampSeriesRows("2024-04-06 01:00:00", "2024-04-07 01:30:00", "2024-04-08 01:30:00")))
	})
}

func TestReferenceTimestamptzSeriesInfinity(t *testing.T) {
	timestamptzSeriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			start, stop, step string
			values            []string
		}{
			{"infinity", "infinity", "1 day", []string{"infinity", "infinity", "infinity"}},
			{"-infinity", "-infinity", "-1 day", []string{"-infinity", "-infinity", "-infinity"}},
			{"-infinity", "2024-01-01+00", "1 day", []string{"-infinity", "-infinity", "-infinity"}},
			{"2024-01-01+00", "infinity", "1 day", []string{"2024-01-01 00:00:00+00", "2024-01-02 00:00:00+00", "2024-01-03 00:00:00+00"}},
			{"infinity", "2024-01-01+00", "1 day", nil},
			{"-infinity", "2024-01-01+00", "-1 day", nil},
		} {
			for _, zone := range []string{"", ",'America/New_York'"} {
				q := "SELECT generate_series(TIMESTAMPTZ '" + tc.start + "',TIMESTAMPTZ '" + tc.stop + "',INTERVAL '" + tc.step + "'" + zone + ")::text AS v LIMIT 3"
				seriesBoth(t, p, b, c, fixture.Select(q, []string{"v"}, []string{"TEXT"}, numericSeriesRows(tc.values...)))
			}
		}
	})
}
