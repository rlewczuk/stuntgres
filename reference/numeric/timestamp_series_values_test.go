package reference_test

import (
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// Independent civil constants, never the implementation's stepping arithmetic.
func timestampSeriesRows(values ...string) [][]any {
	var rows [][]any
	for _, value := range values {
		stamp, err := time.Parse("2006-01-02 15:04:05.999999", value)
		if err != nil {
			panic(err)
		}
		rows = append(rows, []any{stamp})
	}
	return rows
}

func timestampSeriesBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			s.Prepared = prepared
			fixture.TemporalRunStep(t, p.Ctx, c, b.Recorder, s)
		})
	}
}

func TestReferenceTimestampSeriesValues(t *testing.T) {
	seriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			start, stop, step, suffix string
			values                    []string
		}{
			{"2020-02-28", "2020-03-01", "1 day", " ORDER BY v", []string{"2020-02-28 00:00:00", "2020-02-29 00:00:00", "2020-03-01 00:00:00"}},
			{"2021-01-31", "2021-05-31", "1 month", " ORDER BY v", []string{"2021-01-31 00:00:00", "2021-02-28 00:00:00", "2021-03-28 00:00:00", "2021-04-28 00:00:00", "2021-05-28 00:00:00"}},
			{"2021-05-31", "2021-01-01", "-1 month", " ORDER BY v DESC", []string{"2021-05-31 00:00:00", "2021-04-30 00:00:00", "2021-03-30 00:00:00", "2021-02-28 00:00:00", "2021-01-28 00:00:00"}},
			{"2021-01-30 10:00", "2021-03-01 11:00", "1 month 1 day 1 hour", " ORDER BY v", []string{"2021-01-30 10:00:00", "2021-03-01 11:00:00"}},
			{"2020-01-01 00:00:00.000001", "2020-01-01 00:00:00.000005", "2 microseconds", " ORDER BY v", []string{"2020-01-01 00:00:00.000001", "2020-01-01 00:00:00.000003", "2020-01-01 00:00:00.000005"}},
			{"2020-01-03", "2020-01-01", "-1 day", " ORDER BY v DESC", []string{"2020-01-03 00:00:00", "2020-01-02 00:00:00", "2020-01-01 00:00:00"}},
			{"2020-01-01", "2020-01-04", "2 days", " ORDER BY v", []string{"2020-01-01 00:00:00", "2020-01-03 00:00:00"}},
			{"2020-01-01", "2020-01-01", "1 day", "", []string{"2020-01-01 00:00:00"}},
			{"2020-01-01", "2020-01-01", "-1 day", "", []string{"2020-01-01 00:00:00"}},
			{"2020-01-02", "2020-01-01", "1 day", "", nil},
			{"2020-01-01", "2020-01-02", "-1 day", "", nil},
			{"2021-01-31", "2021-02-28", "1 month -29 days", " LIMIT 3", []string{"2021-01-31 00:00:00", "2021-01-30 00:00:00", "2021-01-30 00:00:00"}},
			{"2020-01-01", "2020-01-03", "1 day -1 hour", " ORDER BY v", []string{"2020-01-01 00:00:00", "2020-01-01 23:00:00", "2020-01-02 22:00:00"}},
			{"2020-01-03", "2020-01-01", "-1 day 1 hour", " ORDER BY v DESC", []string{"2020-01-03 00:00:00", "2020-01-02 01:00:00", "2020-01-01 02:00:00"}},
		} {
			t.Run(tc.start+"/"+tc.step+tc.suffix, func(t *testing.T) {
				q := "SELECT generate_series(TIMESTAMP '" + tc.start + "',TIMESTAMP '" + tc.stop + "',INTERVAL '" + tc.step + "') AS v" + tc.suffix
				timestampSeriesBoth(t, p, b, c, fixture.Select(q, []string{"v"}, []string{"TIMESTAMP"}, timestampSeriesRows(tc.values...)))
			})
		}
		for _, zone := range []string{"UTC", "America/New_York"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET TIME ZONE '"+zone+"'", 0))
			for _, tc := range []struct {
				start, stop string
				values      []string
			}{
				{"2020-03-07 01:30", "2020-03-10 01:30", []string{"2020-03-07 01:30:00", "2020-03-08 01:30:00", "2020-03-09 01:30:00", "2020-03-10 01:30:00"}},
				{"2020-10-31 01:30", "2020-11-03 01:30", []string{"2020-10-31 01:30:00", "2020-11-01 01:30:00", "2020-11-02 01:30:00", "2020-11-03 01:30:00"}},
			} {
				q := "SELECT generate_series(TIMESTAMP '" + tc.start + "',TIMESTAMP '" + tc.stop + "',INTERVAL '1 day') AS v ORDER BY v"
				timestampSeriesBoth(t, p, b, c, fixture.Select(q, []string{"v"}, []string{"TIMESTAMP"}, timestampSeriesRows(tc.values...)))
			}
		}
		for _, tc := range []struct {
			args   string
			values []string
		}{
			{"TIMESTAMP 'infinity',TIMESTAMP 'infinity',INTERVAL '1 day'", []string{"infinity", "infinity", "infinity"}},
			{"TIMESTAMP '-infinity',TIMESTAMP '-infinity',INTERVAL '-1 day'", []string{"-infinity", "-infinity", "-infinity"}},
			{"TIMESTAMP '-infinity',TIMESTAMP '2020-01-01',INTERVAL '1 day'", []string{"-infinity", "-infinity", "-infinity"}},
			{"TIMESTAMP '2020-01-01',TIMESTAMP 'infinity',INTERVAL '1 day'", []string{"2020-01-01 00:00:00", "2020-01-02 00:00:00", "2020-01-03 00:00:00"}},
			{"TIMESTAMP 'infinity',TIMESTAMP '2020-01-01',INTERVAL '1 day'", nil},
			{"TIMESTAMP '-infinity',TIMESTAMP '2020-01-01',INTERVAL '-1 day'", nil},
		} {
			// Text casts assert the exact public spelling without assuming pq's raw infinity codec.
			q := "SELECT generate_series(" + tc.args + ")::text AS v LIMIT 3"
			seriesBoth(t, p, b, c, fixture.Select(q, []string{"v"}, []string{"TEXT"}, numericSeriesRows(tc.values...)))
		}
	})
}
