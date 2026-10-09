package reference_test

import (
	"testing"
	"time"
)

func TestReferenceTimezoneDSTAndHistoricalRules(t *testing.T) {
	p := timezoneReferencePair(t)
	for _, tc := range []struct{ input, want string }{
		{"2018-03-11 01:30 America/New_York", "2018-03-11 06:30:00+00"},
		{"2018-03-11 02:30 America/New_York", "2018-03-11 07:30:00+00"},
		{"2018-03-11 03:30 America/New_York", "2018-03-11 07:30:00+00"},
		{"2018-11-04 01:30 America/New_York", "2018-11-04 06:30:00+00"},
		{"2018-11-04 01:30 EDT", "2018-11-04 05:30:00+00"},
		{"2018-10-07 02:15 Australia/Lord_Howe", "2018-10-06 15:45:00+00"},
		{"2018-04-01 01:45 Australia/Lord_Howe", "2018-03-31 15:15:00+00"},
		{"2020-10-04 02:15 Australia/Lord_Howe", "2020-10-03 15:45:00+00"},
		{"2021-04-04 01:45 Australia/Lord_Howe", "2021-04-03 15:15:00+00"},
		{"2004-01-01 12:00-05:30:15", "2004-01-01 17:30:15+00"},
		{"1800-01-01 12:00 Europe/Amsterdam", "1800-01-01 11:40:28+00"},
		{"2010-07-01 12:00 MSK", "2010-07-01 09:00:00+00"},
		{"2010-07-01 12:00 Europe/Moscow", "2010-07-01 08:00:00+00"},
		{"2012-07-01 12:00 MSK", "2012-07-01 08:00:00+00"},
		{"2015-07-01 12:00 MSK", "2015-07-01 09:00:00+00"},
		{"2400-07-01 12:00 America/New_York", "2400-07-01 16:00:00+00"},
		{"2400-01-01 12:00 America/New_York", "2400-01-01 17:00:00+00"},
	} {
		t.Run(tc.input, func(t *testing.T) { timezoneBoth(t, p, timezoneTextStep("TIMESTAMPTZ '"+tc.input+"'", tc.want)) })
	}
	for _, tc := range []struct{ zone, input, want string }{
		{"Asia/Shanghai", "2000-01-01 12:00 CST", "2000-01-01 04:00:00"},
		{"UTC", "2000-01-01 12:00 CST", "2000-01-01 18:00:00"},
		{"Asia/Kolkata", "2000-01-01 12:00 IST", "2000-01-01 06:30:00"},
		{"UTC", "2000-01-01 12:00 IST", "2000-01-01 10:00:00"},
		{"America/New_York", "2000-01-01 12:00 LMT", "2000-01-01 16:56:02"},
	} {
		p.step(t, referenceExec("SET TIME ZONE '"+tc.zone+"'", 0))
		timezoneBoth(t, p, timezoneTextStep("TIMESTAMPTZ '"+tc.input+"' AT TIME ZONE 'UTC'", tc.want))
	}
}

func TestReferenceTimezoneATAndTimetz(t *testing.T) {
	p := timezoneReferencePair(t)
	for _, tc := range []struct{ expression, want string }{
		{"TIMESTAMP '2004-07-01 12:00' AT TIME ZONE 'America/New_York'", "2004-07-01 16:00:00+00"},
		{"TIMESTAMPTZ '2004-07-01 16:00+00' AT TIME ZONE 'America/New_York'", "2004-07-01 12:00:00"},
		{"TIMESTAMP '2004-07-01 12:00' AT TIME ZONE 'America/New_York' AT TIME ZONE 'UTC'", "2004-07-01 16:00:00"},
		{"TIMETZ '05:34:17-05' AT TIME ZONE 'UTC'", "10:34:17+00"},
		{"TIMETZ '23:30+00' AT TIME ZONE 'UTC-2'", "01:30:00+02"},
		{"TIMETZ '2004-07-01 10:00 America/New_York'", "10:00:00-04"},
		{"TIMETZ '2004-01-01 10:00 America/New_York'", "10:00:00-05"},
	} {
		timezoneBoth(t, p, timezoneTextStep(tc.expression, tc.want))
	}
	timezoneBoth(t, p, referenceSelect("SELECT TIMESTAMP '2004-07-01 12:00' AT TIME ZONE $1 AS z FROM tz_witness WHERE id=1", []string{"z"}, []string{"TIMESTAMPTZ"}, [][]any{{time.Date(2004, 7, 1, 16, 0, 0, 0, time.UTC)}}, "America/New_York"))
	timezoneBoth(t, p, referenceSelect("SELECT NULL::TIMESTAMP AT TIME ZONE 'No/Such_Zone' AS z,NULL::TIMESTAMPTZ AT LOCAL AS c,NULL::TIMETZ AT TIME ZONE 'UTC' AS q FROM tz_witness WHERE id=1", []string{"z", "c", "q"}, []string{"TIMESTAMPTZ", "TIMESTAMP", "TIMETZ"}, [][]any{{nil, nil, nil}}))
	timezoneBoth(t, p, timezoneTextStep("CASE WHEN TRUE THEN TIMETZ '05:34:17-05' ELSE TIMETZ '10:00+00' AT TIME ZONE 'No/Such_Zone' END", "05:34:17-05"))
	p.step(t, referenceExec("SET TIME ZONE 'America/New_York'", 0))
	timezoneBoth(t, p, timezoneTextStep("TIMESTAMP '2004-07-01 12:00' AT LOCAL", "2004-07-01 12:00:00-04"))
	timezoneBoth(t, p, timezoneTextStep("TIMETZ '05:34:17-05'", "05:34:17-05"))
}

func TestReferenceTimezoneTimetzTransactionDestinationClock(t *testing.T) {
	p := timezoneReferencePair(t)
	p.step(t, referenceExec("INSERT INTO tz_witness(id) VALUES(2)", 1))
	p.step(t, referenceExec("SET TIME ZONE 'America/New_York'", 0))
	for _, backend := range p.backends() {
		tx, err := backend.db.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		clock := timezoneTransactionClock(t, p, tx, backend.recorder)
		sunday := func(month time.Month, n, hour int) time.Time {
			first := time.Date(clock.UTC().Year(), month, 1, hour, 0, 0, 0, time.UTC)
			return first.AddDate(0, 0, (7-int(first.Weekday()))%7+7*(n-1))
		}
		want := "05:34:17-05"
		if !clock.Before(sunday(time.March, 2, 7)) && clock.Before(sunday(time.November, 1, 6)) {
			want = "06:34:17-04"
		}
		for _, prepared := range []bool{false, true} {
			var raw any = want
			if backend.pg {
				raw = time.Date(0, 1, 1, 10, 34, 17, 0, time.UTC)
			}
			rawStep := referenceSelect("SELECT TIMETZ '05:34:17-05' AT TIME ZONE 'America/New_York' AS q FROM tz_witness ORDER BY id", []string{"q"}, []string{"TIMETZ"}, [][]any{{raw}, {raw}})
			rawStep.prepared = prepared
			referenceRunStep(t, p.ctx, tx, backend.recorder, rawStep)
			for _, expr := range []string{
				"TIMETZ '05:34:17-05' AT TIME ZONE 'America/New_York'",
				"TIMETZ '2004-01-01 05:34:17-05' AT TIME ZONE 'America/New_York'",
				"TIMETZ '2004-07-01 05:34:17-05' AT LOCAL",
			} {
				s := referenceSelect("SELECT ("+expr+")::TEXT AS q FROM tz_witness ORDER BY id", []string{"q"}, []string{"TEXT"}, [][]any{{want}, {want}})
				s.prepared = prepared
				referenceRunStep(t, p.ctx, tx, backend.recorder, s)
			}
			s := referenceSelect("SELECT (TIMETZ '2004-07-01 12:00 America/New_York')::TEXT AS summer,(TIMETZ '2004-01-01 12:00 America/New_York')::TEXT AS winter FROM tz_witness WHERE id=1", []string{"summer", "winter"}, []string{"TEXT", "TEXT"}, [][]any{{"12:00:00-04", "12:00:00-05"}})
			s.prepared = prepared
			referenceRunStep(t, p.ctx, tx, backend.recorder, s)
			var summer, winter any = "12:00:00-04", "12:00:00-05"
			if backend.pg {
				summer = time.Date(0, 1, 1, 16, 0, 0, 0, time.UTC)
				winter = time.Date(0, 1, 1, 17, 0, 0, 0, time.UTC)
			}
			rawDates := referenceSelect("SELECT TIMETZ '2004-07-01 12:00 America/New_York' AS summer,TIMETZ '2004-01-01 12:00 America/New_York' AS winter FROM tz_witness WHERE id=1", []string{"summer", "winter"}, []string{"TIMETZ", "TIMETZ"}, [][]any{{summer, winter}})
			rawDates.prepared = prepared
			referenceRunStep(t, p.ctx, tx, backend.recorder, rawDates)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferenceTimezoneComparisonAndComposition(t *testing.T) {
	p := timezoneReferencePair(t)
	for _, zone := range []string{"America/New_York", "UTC"} {
		p.step(t, referenceExec("SET TIME ZONE '"+zone+"'", 0))
		equal := zone == "America/New_York"
		timezoneBoth(t, p, referenceSelect("SELECT DATE '2004-10-19'=TIMESTAMPTZ '2004-10-19 04:00+00' AS a,TIMESTAMPTZ '2004-10-19 04:00+00'=DATE '2004-10-19' AS b,TIMESTAMP '2004-10-19 00:00'=TIMESTAMPTZ '2004-10-19 04:00+00' AS c FROM tz_witness WHERE id=1", []string{"a", "b", "c"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{equal, equal, equal}}))
	}
	timezoneBoth(t, p, referenceSelect("SELECT TIMETZ '09:00+01'=TIMETZ '08:00+00' AS a,TIMETZ '09:00+01'<TIMETZ '08:00+00' AS b,TIMESTAMPTZ '-infinity'<TIMESTAMPTZ 'epoch' AS c,TIMESTAMPTZ 'infinity'>TIMESTAMPTZ 'epoch' AS d,DATE '5874897-12-31'>TIMESTAMPTZ '294276-12-31+00' AS e FROM tz_witness WHERE id=1", []string{"a", "b", "c", "d", "e"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{false, true, true, true, true}}))
	p.step(t, referenceExec("INSERT INTO tz_witness(id,z,q) VALUES(2,'2004-10-19 10:00+02','09:00+01'),(3,'2004-10-19 08:00+00','08:00+00'),(4,'2004-10-19 08:00+00','09:00+01')", 3))
	timezoneBoth(t, p, referenceSelect("SELECT q::TEXT AS v FROM tz_witness WHERE q IS NOT NULL ORDER BY q", []string{"v"}, []string{"TEXT"}, [][]any{{"09:00:00+01"}, {"09:00:00+01"}, {"08:00:00+00"}}))
	timezoneBoth(t, p, referenceSelect("SELECT q::TEXT AS v,COUNT(*) AS n FROM tz_witness WHERE q IS NOT NULL GROUP BY q ORDER BY q", []string{"v", "n"}, []string{"TEXT", "INT8"}, [][]any{{"09:00:00+01", int64(2)}, {"08:00:00+00", int64(1)}}))
	// DISTINCT and MIN/MAX are not baseline SQL consumers. Group by the
	// temporal value (not its formatted text), and assert both ordered bounds.
	timezoneBoth(t, p, referenceSelect("SELECT z::TEXT AS v,COUNT(*) AS n FROM tz_witness WHERE z IS NOT NULL GROUP BY z", []string{"v", "n"}, []string{"TEXT", "INT8"}, [][]any{{"2004-10-19 08:00:00+00", int64(3)}}))
	timezoneBoth(t, p, referenceSelect("SELECT q::TEXT AS v,q>=TIMETZ '09:00+01' AS lo,q<=TIMETZ '08:00+00' AS hi FROM tz_witness WHERE q IS NOT NULL ORDER BY q DESC", []string{"v", "lo", "hi"}, []string{"TEXT", "BOOL", "BOOL"}, [][]any{{"08:00:00+00", true, true}, {"09:00:00+01", true, true}, {"09:00:00+01", true, true}}))
	timezoneBoth(t, p, referenceSelect("SELECT NULLIF(z,TIMESTAMPTZ '2004-10-19 08:00+00') AS n,COALESCE(z,TIMESTAMPTZ 'epoch') IS DISTINCT FROM NULL AS b,z BETWEEN TIMESTAMPTZ '2004-01-01+00' AND TIMESTAMPTZ '2005-01-01+00' AS c FROM tz_witness WHERE id=2", []string{"n", "b", "c"}, []string{"TIMESTAMPTZ", "BOOL", "BOOL"}, [][]any{{nil, true, true}}))
	timezoneBoth(t, p, referenceSelect("SELECT a.id AS id FROM tz_witness a JOIN tz_witness b(bid,bz,bq) ON a.z=b.bz WHERE b.bid=2 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}, {int64(4)}}))
}
