package stuntgres_test

import (
	"context"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
	"time"
	"unicode/utf8"
)

func TestTimezoneDiagnosticsPhasesAndAtomicity(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	c := timezoneConn(t, db)
	ctx := context.Background()
	timezoneExec(t, c, "CREATE TABLE tz_witness(z TIMESTAMPTZ,q TIMETZ)", 0)
	timezoneExec(t, c, "INSERT INTO tz_witness VALUES('2004-01-01+00','12:00+00')", 1)
	for _, tc := range []struct{ sql, code, message, position, hint string }{
		{"SELECT TIMESTAMPTZ 'nonsense' FROM tz_witness", "22007", "invalid input syntax for type timestamp with time zone: \"nonsense\"", "20", ""},
		{"SELECT TIMESTAMPTZ '2004-10-19 10:00+16' FROM tz_witness", "22009", "time zone displacement out of range: \"2004-10-19 10:00+16\"", "20", ""},
		{"SELECT TIMESTAMPTZ '2004-10-19 10:00 No/Such_Zone' FROM tz_witness", "22023", "time zone \"no/such_zone\" not recognized", "20", ""},
		{"SELECT TIMETZ '10:00 America/New_York' FROM tz_witness", "22007", "invalid input syntax for type time with time zone: \"10:00 America/New_York\"", "15", ""},
		{"SELECT DATE '31/12/2024' FROM tz_witness", "22008", "date/time field value out of range: \"31/12/2024\"", "13", "Perhaps you need a different \"DateStyle\" setting."},
		{"SET TIME ZONE = 'UTC'", "42601", "syntax error at or near \"=\"", "15", ""},
		{"SET TIME ZONE INTERVAL '1' DAY", "42601", "time zone interval must be HOUR or HOUR TO MINUTE", "28", ""},
	} {
		want := stuntgres.Error{Code: tc.code, Message: tc.message, Position: tc.position, Hint: tc.hint}
		r, err := c.QueryContext(ctx, tc.sql)
		if r != nil {
			r.Close()
			t.Fatal("failed query returned cursor")
		}
		timeDiagnostic(t, err, want)
		s, err := c.PrepareContext(ctx, tc.sql)
		if s != nil {
			s.Close()
			t.Fatal("invalid constant prepared")
		}
		timeDiagnostic(t, err, want)
	}
	for _, tc := range []struct{ sql, message, detail string }{
		{"SET TIME ZONE 'No/Such_Zone'", "invalid value for parameter \"TimeZone\": \"No/Such_Zone\"", ""},
		{"SET DateStyle='ISO,SQL'", "invalid value for parameter \"DateStyle\": \"ISO,SQL\"", "Conflicting \"DateStyle\" specifications."},
		{"SET DateStyle='nonsense'", "invalid value for parameter \"DateStyle\": \"nonsense\"", "Unrecognized key word: \"nonsense\"."},
		{"SET DateStyle='ISO,,MDY'", "invalid value for parameter \"DateStyle\": \"ISO,,MDY\"", "List syntax is invalid."},
		{"SET TIME ZONE 168", "invalid value for parameter \"TimeZone\": \"168\"", "UTC timezone offset is out of range."},
		{"SET TIME ZONE INTERVAL '1 month'", "invalid value for parameter \"TimeZone\": \"INTERVAL '1 mon'\"", "Cannot specify months in time zone interval."},
		{"SET TIME ZONE INTERVAL '1 day'", "invalid value for parameter \"TimeZone\": \"INTERVAL '1 day'\"", "Cannot specify days in time zone interval."},
	} {
		want := stuntgres.Error{Code: "22023", Message: tc.message, Detail: tc.detail}
		_, err := c.ExecContext(ctx, tc.sql)
		timeDiagnostic(t, err, want)
		s, err := c.PrepareContext(ctx, tc.sql)
		if err != nil {
			t.Fatal("setting validation happened during Prepare", err)
		}
		_, err = s.ExecContext(ctx)
		s.Close()
		timeDiagnostic(t, err, want)
		timezoneRows(t, c, "SHOW TimeZone", []string{"TEXT"}, [][]any{{"UTC"}})
		timezoneRows(t, c, "SHOW DateStyle", []string{"TEXT"}, [][]any{{"ISO, MDY"}})
	}
	s, err := c.PrepareContext(ctx, "INSERT INTO tz_witness(z) VALUES($1)")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.ExecContext(ctx, "nonsense")
	timeDiagnostic(t, err, stuntgres.Error{Code: "22007", Message: "invalid input syntax for type timestamp with time zone: \"nonsense\""})
	q := "/*é*/ SELECT TIMESTAMPTZ 'nonsense' FROM tz_witness"
	_, err = c.QueryContext(ctx, q)
	timeDiagnostic(t, err, stuntgres.Error{Code: "22007", Message: "invalid input syntax for type timestamp with time zone: \"nonsense\"", Position: strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, "'nonsense'")]) + 1)})
	for _, q := range []string{"INSERT INTO tz_witness(z) VALUES($1),($2)", "UPDATE tz_witness SET z=$1 WHERE q=$2"} {
		if strings.HasPrefix(q, "INSERT") {
			_, err = c.ExecContext(ctx, q, "2005-01-01+00", "nonsense")
		} else {
			_, err = c.ExecContext(ctx, q, "nonsense", "12:00+00")
		}
		timeDiagnostic(t, err, stuntgres.Error{Code: "22007", Message: "invalid input syntax for type timestamp with time zone: \"nonsense\""})
	}
	timezoneRows(t, c, "SELECT z::TEXT,q FROM tz_witness", []string{"TEXT", "TIMETZ"}, [][]any{{"2004-01-01 00:00:00+00", "12:00:00+00"}})
}

func TestTimezoneTimetzTransactionDestinationClock(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	c := timezoneConn(t, db)
	timezoneExec(t, c, "CREATE TABLE tz_witness(v INTEGER)", 0)
	timezoneExec(t, c, "INSERT INTO tz_witness VALUES(1),(2)", 2)
	timezoneExec(t, c, "BEGIN", 0)
	defer c.ExecContext(context.Background(), "ROLLBACK")
	var instant time.Time
	if err := c.QueryRowContext(context.Background(), "SELECT TIMESTAMPTZ 'now' FROM tz_witness WHERE v=1").Scan(&instant); err != nil {
		t.Fatal(err)
	}
	// Current US rules: second Sunday March 07:00 UTC through first Sunday November 06:00 UTC.
	sunday := func(month time.Month, n int, hour int) time.Time {
		d := time.Date(instant.Year(), month, 1, hour, 0, 0, 0, time.UTC)
		return d.AddDate(0, 0, (7-int(d.Weekday()))%7+7*(n-1))
	}
	start, end := sunday(time.March, 2, 7), sunday(time.November, 1, 6)
	want := "05:34:17-05"
	if !instant.Before(start) && instant.Before(end) {
		want = "06:34:17-04"
	}
	timezoneRows(t, c, "SELECT TIMETZ '05:34:17-05' AT TIME ZONE 'America/New_York' FROM tz_witness ORDER BY v", []string{"TIMETZ"}, [][]any{{want}, {want}})
	timezoneRows(t, c, "SELECT TIMETZ '2004-07-01 12:00 America/New_York',TIMETZ '2004-01-01 12:00 America/New_York' FROM tz_witness WHERE v=1", []string{"TIMETZ", "TIMETZ"}, [][]any{{"12:00:00-04", "12:00:00-05"}})
}

func TestTimezoneRuntimeZoneAndIntervalDiagnostics(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	c := timezoneConn(t, db)
	timezoneExec(t, c, "CREATE TABLE tz_witness(v TIMESTAMP)", 0)
	timezoneExec(t, c, "INSERT INTO tz_witness VALUES('2004-10-19 10:00')", 1)
	s, err := c.PrepareContext(ctx, "SELECT v AT TIME ZONE $1 FROM tz_witness")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, zone := range []string{"No/Such_Zone", "bad/zone"} {
		r, err := s.QueryContext(ctx, zone)
		if r != nil {
			r.Close()
			t.Fatal("bad zone returned cursor")
		}
		timeDiagnostic(t, err, stuntgres.Error{Code: "22023", Message: "time zone \"" + zone + "\" not recognized"})
	}
	timezoneRows(t, c, "SELECT (v AT TIME ZONE $1)::TEXT FROM tz_witness", []string{"TEXT"}, [][]any{{"2004-10-19 10:00:00+00"}}, "UTC")
	for _, tc := range []struct{ sql, code, message, detail string }{
		{"SET TIME ZONE INTERVAL '168 hours'", "22023", "invalid value for parameter \"TimeZone\": \"INTERVAL '168:00:00'\"", "UTC timezone offset is out of range."},
		{"SET TIME ZONE INTERVAL 'nonsense'", "22007", "invalid input syntax for type interval: \"nonsense\"", ""},
	} {
		p, err := c.PrepareContext(ctx, tc.sql)
		if err != nil {
			t.Fatal("interval validation at Prepare", err)
		}
		_, err = p.ExecContext(ctx)
		p.Close()
		timeDiagnostic(t, err, stuntgres.Error{Code: tc.code, Message: tc.message, Detail: tc.detail})
		timezoneRows(t, c, "SHOW TimeZone", []string{"TEXT"}, [][]any{{"UTC"}})
	}
}
