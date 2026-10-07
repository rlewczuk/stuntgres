//go:build reference

package reference_test

import (
	"fmt"
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
	// Only the oracle supplies clock evidence; expected NY DST is calculated from
	// US transition dates, never from host zone data or the conversion under test.
	for _, prepared := range []bool{false, true} {
		pg, err := p.postgres.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		var clock time.Time
		if err := pg.QueryRowContext(p.ctx, "SELECT transaction_timestamp()").Scan(&clock); err != nil {
			pg.Rollback()
			t.Fatal(err)
		}
		native, err := p.memory.BeginTx(p.ctx, nil)
		if err != nil {
			pg.Rollback()
			t.Fatal(err)
		}
		year := clock.UTC().Year()
		sunday := func(month time.Month, n int) time.Time {
			first := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
			return first.AddDate(0, 0, (7-int(first.Weekday()))%7+7*(n-1))
		}
		start := sunday(time.March, 2).Add(7 * time.Hour)
		end := sunday(time.November, 1).Add(6 * time.Hour)
		offset := -5
		if !clock.Before(start) && clock.Before(end) {
			offset = -4
		}
		// Transactions cannot share an injected public clock. Refuse to claim a
		// season witness when their start instants straddle the actual transition.
		now := time.Now().UTC()
		if (clock.Before(start) != now.Before(start)) || (clock.Before(end) != now.Before(end)) {
			native.Rollback()
			pg.Rollback()
			t.Fatal("transaction starts straddle NY DST transition")
		}
		want := fmt.Sprintf("%02d:34:17-%02d", 10+offset, -offset)
		s := timezoneTextStep("TIMETZ '2004-01-01 05:34:17-05' AT TIME ZONE 'America/New_York'", want)
		s.prepared = prepared
		referenceRunStep(t, p.ctx, native, p.memoryRecord, s)
		referenceRunStep(t, p.ctx, pg, p.postgresRecord, s)
		s = timezoneTextStep("TIMETZ '2004-07-01 05:34:17-05' AT LOCAL", want)
		s.prepared = prepared
		referenceRunStep(t, p.ctx, native, p.memoryRecord, s)
		referenceRunStep(t, p.ctx, pg, p.postgresRecord, s)
		if err := native.Rollback(); err != nil {
			t.Fatal(err)
		}
		if err := pg.Rollback(); err != nil {
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
	timezoneBoth(t, p, referenceSelect("SELECT DISTINCT z::TEXT AS v FROM tz_witness WHERE z IS NOT NULL", []string{"v"}, []string{"TEXT"}, [][]any{{"2004-10-19 08:00:00+00"}}))
	timezoneBoth(t, p, referenceSelect("SELECT MIN(q)::TEXT AS lo,MAX(q)::TEXT AS hi FROM tz_witness", []string{"lo", "hi"}, []string{"TEXT", "TEXT"}, [][]any{{"09:00:00+01", "08:00:00+00"}}))
	timezoneBoth(t, p, referenceSelect("SELECT NULLIF(z,TIMESTAMPTZ '2004-10-19 08:00+00') AS n,COALESCE(z,TIMESTAMPTZ 'epoch') IS DISTINCT FROM NULL AS b,z BETWEEN TIMESTAMPTZ '2004-01-01+00' AND TIMESTAMPTZ '2005-01-01+00' AS c FROM tz_witness WHERE id=2", []string{"n", "b", "c"}, []string{"TIMESTAMPTZ", "BOOL", "BOOL"}, [][]any{{nil, true, true}}))
	timezoneBoth(t, p, referenceSelect("SELECT a.id AS id FROM tz_witness a JOIN tz_witness b ON a.z=b.z WHERE b.id=2 ORDER BY a.id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}, {int64(4)}}))
}
