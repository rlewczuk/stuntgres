package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func datetimeZoneOffsets(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	for _, tc := range []struct{ value, zone, want string }{
		{"TIMESTAMPTZ '2001-02-16 20:38:40-05'", "'+05'", "2001-02-16 20:38:40"},
		{"TIMESTAMPTZ '2001-02-16 20:38:40-05'", "INTERVAL '5 hours'", "2001-02-17 06:38:40"},
		{"TIMESTAMP '2004-01-01 12:00'", "INTERVAL '1.9 seconds'", "2004-01-01 11:59:59+00"},
		{"TIMESTAMP '2004-01-01 12:00'", "INTERVAL '-1.9 seconds'", "2004-01-01 12:00:01+00"},
		{"TIMESTAMPTZ '2004-01-01 12:00+00'", "INTERVAL '1.9 seconds'", "2004-01-01 12:00:01"},
		{"TIMESTAMPTZ '2004-01-01 12:00+00'", "INTERVAL '-1.9 seconds'", "2004-01-01 11:59:59"},
		{"TIMETZ '23:30+00'", "INTERVAL '2 hours'", "01:30:00+02"},
		{"TIMETZ '24:00+00'", "'UTC'", "00:00:00+00"},
		{"TIMETZ '12:00+00'", "INTERVAL '1.9 seconds'", "12:00:01+00:00:01"},
		{"TIMETZ '12:00+00'", "INTERVAL '-1.9 seconds'", "11:59:59-00:00:01"},
		{"TIMETZ '12:00+00'", "INTERVAL '24 hours'", "12:00:00+24"},
		{"TIMETZ '12:00+00'", "INTERVAL '-24 hours'", "12:00:00-24"},
		{"TIMESTAMP '2018-03-11 02:30'", "'America/New_York'", "2018-03-11 07:30:00+00"},
		{"TIMESTAMP '2018-11-04 01:30'", "'America/New_York'", "2018-11-04 06:30:00+00"},
		{"TIMESTAMP '2018-11-04 01:30'", "'EDT'", "2018-11-04 05:30:00+00"},
		{"TIMESTAMP '2018-10-07 02:15'", "'Australia/Lord_Howe'", "2018-10-06 15:45:00+00"},
		{"TIMESTAMP '1800-01-01 12:00'", "'Europe/Amsterdam'", "1800-01-01 11:40:28+00"},
		{"TIMESTAMP '2010-07-01 12:00'", "'MSK'", "2010-07-01 09:00:00+00"},
		{"TIMESTAMP '2004-07-01 12:00'", "'aMeRiCa/NeW_yOrK'", "2004-07-01 16:00:00+00"},
		{"TIMESTAMP '2004-07-01 12:00'", "'EST'", "2004-07-01 17:00:00+00"},
	} {
		t.Run(tc.value+"_"+tc.zone, func(t *testing.T) {
			for _, expr := range []string{"timezone(" + tc.zone + "," + tc.value + ")", tc.value + " AT TIME ZONE " + tc.zone} {
				datetimeZoneText(t, p, b, c, expr, tc.want)
			}
		})
	}
	datetimeZoneText(t, p, b, c, "TIMESTAMP '2001-02-16 20:38:40' AT TIME ZONE 'Asia/Tokyo' AT TIME ZONE 'America/Chicago'", "2001-02-16 05:38:40")
	datetimeZoneText(t, p, b, c, "TIMESTAMP '2004-07-01 12:00' AT TIME ZONE 'UTC'+INTERVAL '1 hour'", "2004-07-01 13:00:00+00")
	for _, typ := range []string{"TIMESTAMP", "TIMESTAMPTZ"} {
		for _, infinity := range []string{"infinity", "-infinity"} {
			for _, zone := range []string{"'No/Such_Zone'", "INTERVAL '1 month'", "INTERVAL '1 day'", "INTERVAL 'infinity'"} {
				for _, expr := range []string{"timezone(" + zone + "," + typ + " '" + infinity + "')", typ + " '" + infinity + "' AT TIME ZONE " + zone} {
					datetimeZoneText(t, p, b, c, expr, infinity)
				}
			}
		}
	}
}

func TestReferenceDatetimeZoneOffsetsAndDST(t *testing.T) {
	datetimeZoneScenario(t, datetimeZoneOffsets)
}

func datetimeZoneCalendar(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	for _, tc := range []struct{ fn, source, amount, zone, want string }{
		{"date_add", "2021-10-31 00:00+02", "1 day", "Europe/Warsaw", "2021-10-31 23:00:00+00"},
		{"date_add", "2021-10-31 00:00+02", "24 hours", "Europe/Warsaw", "2021-10-31 22:00:00+00"},
		{"date_subtract", "2021-11-01 00:00+01", "1 day", "Europe/Warsaw", "2021-10-30 22:00:00+00"},
		{"date_subtract", "2021-11-01 00:00+01", "24 hours", "Europe/Warsaw", "2021-10-30 23:00:00+00"},
		{"date_add", "2005-04-02 12:00-07", "1 day", "America/Denver", "2005-04-03 18:00:00+00"},
		{"date_add", "2005-04-02 12:00-07", "24 hours", "America/Denver", "2005-04-03 19:00:00+00"},
		{"date_add", "2005-04-02 12:00-07", "1 day", "UTC", "2005-04-03 19:00:00+00"},
		{"date_add", "2024-01-31 12:00:00.123456+00", "1 month 1 day 1 microsecond", "UTC", "2024-03-01 12:00:00.123457+00"},
		{"date_add", "2023-01-31 12:00+00", "1 month", "UTC", "2023-02-28 12:00:00+00"},
		{"date_add", "2024-03-31 12:00+00", "2 months", "UTC", "2024-05-31 12:00:00+00"},
		{"date_add", "2018-02-11 02:30-05", "1 month 1 day", "America/New_York", "2018-03-12 07:30:00+00"},
		{"date_subtract", "2018-04-11 02:30-04", "1 month 1 day", "America/New_York", "2018-03-10 08:30:00+00"},
		{"date_add", "2018-10-04 01:30-04", "1 month 1 day", "America/New_York", "2018-11-05 06:30:00+00"},
		{"date_subtract", "2018-12-04 01:30-05", "1 month 1 day", "America/New_York", "2018-11-03 05:30:00+00"},
		{"date_add", "2010-06-30 08:00+00", "1 day", "MSK", "2010-07-01 08:00:00+00"},
		{"date_add", "2004-01-01 12:00+00", "1 day", "EST", "2004-01-02 12:00:00+00"},
		{"date_subtract", "2004-01-01 12:00+00", "1 day", "+05", "2003-12-31 12:00:00+00"},
		{"date_add", "infinity", "1 day", "UTC", "infinity"},
		{"date_subtract", "-infinity", "1 day", "UTC", "-infinity"},
		{"date_add", "2024-01-01+00", "infinity", "UTC", "infinity"},
		{"date_subtract", "2024-01-01+00", "infinity", "UTC", "-infinity"},
	} {
		t.Run(tc.fn+"_"+tc.source+"_"+tc.amount+"_"+tc.zone, func(t *testing.T) {
			expr := tc.fn + "(TIMESTAMPTZ '" + tc.source + "',INTERVAL '" + tc.amount + "','" + tc.zone + "')"
			datetimeZoneText(t, p, b, c, expr, tc.want)
		})
	}
	for _, fn := range []string{"date_add", "date_subtract"} {
		op := "+"
		if fn == "date_subtract" {
			op = "-"
		}
		want := "2005-04-03 19:00:00+00"
		if fn == "date_subtract" {
			want = "2005-04-01 19:00:00+00"
		}
		datetimeZoneText(t, p, b, c, fn+"(TIMESTAMPTZ '2005-04-02 12:00-07',INTERVAL '1 day')", want)
		datetimeZoneText(t, p, b, c, "TIMESTAMPTZ '2005-04-02 12:00-07'"+op+"INTERVAL '1 day'", want)
	}
}

func TestReferenceDatetimeZoneCalendarStagesAndBoundaries(t *testing.T) {
	datetimeZoneScenario(t, datetimeZoneCalendar)
}

func datetimeZoneExecution(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	for _, tc := range []struct{ expr, ny, utc string }{
		{"timezone(TIMESTAMP '2004-07-01 12:00')", "2004-07-01 12:00:00-04", "2004-07-01 12:00:00+00"},
		{"TIMESTAMP '2004-07-01 12:00' AT LOCAL", "2004-07-01 12:00:00-04", "2004-07-01 12:00:00+00"},
		{"timezone(TIMESTAMPTZ '2004-07-01 16:00+00')", "2004-07-01 12:00:00", "2004-07-01 16:00:00"},
		{"TIMESTAMPTZ '2004-07-01 16:00+00' AT LOCAL", "2004-07-01 12:00:00", "2004-07-01 16:00:00"},
		{"date_add(TIMESTAMPTZ '2005-04-02 12:00-07',INTERVAL '1 day')", "2005-04-03 14:00:00-04", "2005-04-03 19:00:00+00"},
		{"date_add(TIMESTAMPTZ '2005-04-02 12:00-07',INTERVAL '1 day','America/Denver')", "2005-04-03 14:00:00-04", "2005-04-03 18:00:00+00"},
		{"date_subtract(TIMESTAMPTZ '2005-04-04 12:00-06',INTERVAL '1 day','America/Denver')", "2005-04-03 14:00:00-04", "2005-04-03 18:00:00+00"},
	} {
		q := "SELECT (" + tc.expr + ")::TEXT AS v FROM tz_witness WHERE id=1"
		h := timezoneHeld(t, p, c, q)
		for _, z := range []struct{ zone, want string }{{"America/New_York", tc.ny}, {"UTC", tc.utc}} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET TIME ZONE '"+z.zone+"'", 0))
			fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{z.want}}))
		}
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO tz_witness(id) VALUES(2)", 1))
	tx, err := c.BeginTx(p.Ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	clock := timezoneTransactionClock(t, p, tx, b.Recorder)
	sunday := func(month time.Month, n, hour int) time.Time {
		first := time.Date(clock.Year(), month, 1, hour, 0, 0, 0, time.UTC)
		return first.AddDate(0, 0, (7-int(first.Weekday()))%7+7*(n-1))
	}
	want := "05:34:17-05"
	if !clock.Before(sunday(time.March, 2, 7)) && clock.Before(sunday(time.November, 1, 6)) {
		want = "06:34:17-04"
	}
	fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("SET TIME ZONE 'America/New_York'", 0))
	for _, expr := range []string{
		"timezone('America/New_York',TIMETZ '05:34:17-05')",
		"timezone('America/New_York',TIMETZ '2004-01-01 05:34:17-05')",
		"timezone(TIMETZ '2004-07-01 05:34:17-05')",
		"TIMETZ '2004-07-01 05:34:17-05' AT LOCAL",
	} {
		datetimeArithmeticBoth(t, p, b, tx, fixture.Select("SELECT ("+expr+")::TEXT AS v FROM tz_witness ORDER BY id", []string{"v"}, []string{"TEXT"}, [][]any{{want}, {want}}))
	}
	fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("SET TIME ZONE 'UTC'", 0))
	datetimeArithmeticBoth(t, p, b, tx, fixture.Select("SELECT timezone(TIMETZ '05:34:17-05')::TEXT AS v FROM tz_witness ORDER BY id", []string{"v"}, []string{"TEXT"}, [][]any{{"10:34:17+00"}, {"10:34:17+00"}}))
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
}

func TestReferenceDatetimeZoneCurrentTimetzAndSessionExecution(t *testing.T) {
	datetimeZoneScenario(t, datetimeZoneExecution)
}
