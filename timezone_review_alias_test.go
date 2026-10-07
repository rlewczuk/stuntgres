package stuntgres_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"stuntgres"
	"testing"
)

func TestTimezoneReviewAliasSettings(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	c := timezoneConn(t, db)
	timezoneExec(t, c, "CREATE TABLE tz_witness(v INTEGER)", 0)
	timezoneExec(t, c, "INSERT INTO tz_witness VALUES(1)", 1)
	for _, tc := range []struct{ number, aliases string }{
		{"1", "h hr hrs hour hours HRS"},
		{"60", "m min mins minute minutes"},
		{"3600", "s sec secs second seconds"},
		{"3600000", "ms msec msecs msecond mseconds millisecon millisecond milliseconds millisecondsuffix"},
		{"3600000000", "us usec usecs usecond useconds microsecon microsecond microseconds microsecondsuffix"},
	} {
		for _, alias := range strings.Fields(tc.aliases) {
			for _, separator := range []string{"", " "} {
				input := tc.number + separator + alias
				t.Run(input, func(t *testing.T) {
					for _, prepared := range []bool{false, true} {
						for _, q := range []string{"SET TIME ZONE INTERVAL '" + input + "'", "SET timezone='INTERVAL ''" + input + "'''", "SELECT set_config('timezone','INTERVAL ''" + input + "''',false)"} {
							timezoneExec(t, c, "SET TIME ZONE 'America/New_York'", 0)
							timezoneAliasRun(t, c, q, prepared, nil, strings.HasPrefix(q, "SELECT"))
							timezoneAliasRun(t, c, "SHOW TimeZone", prepared, nil, true)
							conversion := "SELECT ('2004-07-01 12:00'::TEXT::TIMESTAMPTZ AT TIME ZONE 'UTC')::TEXT FROM tz_witness"
							timezoneAliasRun(t, c, conversion, prepared, nil, true)
						}
					}
				})
			}
		}
	}
	for _, tc := range []struct{ input, normalized, detail string }{
		{"1 c", "100 years", "Cannot specify months in time zone interval."},
		{"1 cent", "100 years", "Cannot specify months in time zone interval."},
		{"1 dec", "10 years", "Cannot specify months in time zone interval."},
		{"1 decs", "10 years", "Cannot specify months in time zone interval."},
		{"1 mil", "1000 years", "Cannot specify months in time zone interval."},
		{"1 mils", "1000 years", "Cannot specify months in time zone interval."},
		{"1 yr", "1 year", "Cannot specify months in time zone interval."},
		{"1 yrs", "1 year", "Cannot specify months in time zone interval."},
		{"1 mons", "1 mon", "Cannot specify months in time zone interval."},
		{"1 w", "7 days", "Cannot specify days in time zone interval."},
		{"1 d", "1 day", "Cannot specify days in time zone interval."},
		{"1 century", "100 years", "Cannot specify months in time zone interval."},
		{"1 centuries", "100 years", "Cannot specify months in time zone interval."},
		{"1 decade", "10 years", "Cannot specify months in time zone interval."},
		{"1 decades", "10 years", "Cannot specify months in time zone interval."},
		{"1 millennia", "1000 years", "Cannot specify months in time zone interval."},
		{"1 millennium", "1000 years", "Cannot specify months in time zone interval."},
		{"1 millenniums", "1000 years", "Cannot specify months in time zone interval."},
		{"1 y", "1 year", "Cannot specify months in time zone interval."},
		{"1 year", "1 year", "Cannot specify months in time zone interval."},
		{"1 years", "1 year", "Cannot specify months in time zone interval."},
		{"1 mon", "1 mon", "Cannot specify months in time zone interval."},
		{"1 month", "1 mon", "Cannot specify months in time zone interval."},
		{"1 months", "1 mon", "Cannot specify months in time zone interval."},
		{"1 week", "7 days", "Cannot specify days in time zone interval."},
		{"1 weeks", "7 days", "Cannot specify days in time zone interval."},
		{"1 day", "1 day", "Cannot specify days in time zone interval."},
		{"1 days", "1 day", "Cannot specify days in time zone interval."},
		{"1 hr 1 day 1 mon", "1 mon 1 day 01:00:00", "Cannot specify months in time zone interval."},
		{"168 hrs", "168:00:00", "UTC timezone offset is out of range."},
		{"1 hrr", "", ""}, {"1 msecx", "", ""}, {"1 usecx", "", ""}, {"1 hr 2 hours", "", ""}, {"@ ago", "", ""}, {"9223372036854775808 useconds", "", ""},
	} {
		for _, prepared := range []bool{false, true} {
			for _, q := range []string{"SET TIME ZONE INTERVAL '" + tc.input + "'", "SET timezone='INTERVAL ''" + tc.input + "'''", "SELECT set_config('timezone','INTERVAL ''" + tc.input + "''',false)"} {
				timezoneExec(t, c, "SET TIME ZONE 'America/New_York'", 0)
				want := stuntgres.Error{Code: "22007", Message: "invalid input syntax for type interval: \"" + tc.input + "\""}
				if strings.HasPrefix(tc.input, "922337") {
					want.Code, want.Message = "22015", "interval field value out of range: \""+tc.input+"\""
				} else if tc.detail != "" {
					value := tc.input
					if strings.HasPrefix(q, "SET TIME ZONE") {
						value = tc.normalized
					}
					want = stuntgres.Error{Code: "22023", Message: "invalid value for parameter \"TimeZone\": \"INTERVAL '" + value + "'\"", Detail: tc.detail}
				}
				timezoneAliasRun(t, c, q, prepared, &want, strings.HasPrefix(q, "SELECT"))
				timezoneRows(t, c, "SHOW TimeZone", []string{"TEXT"}, [][]any{{"America/New_York"}})
				timezoneRows(t, c, "SELECT ('2004-07-01 12:00'::TEXT::TIMESTAMPTZ AT TIME ZONE 'UTC')::TEXT FROM tz_witness", []string{"TEXT"}, [][]any{{"2004-07-01 16:00:00"}})
			}
		}
	}
}

func timezoneAliasRun(t *testing.T, c *sql.Conn, q string, prepared bool, wantError *stuntgres.Error, query bool) {
	t.Helper()
	ctx := context.Background()
	var rows *sql.Rows
	var result sql.Result
	var err error
	if prepared {
		s, prepareErr := c.PrepareContext(ctx, q)
		if prepareErr != nil {
			t.Fatalf("prepare %s: %v", q, prepareErr)
		}
		defer s.Close()
		if query {
			rows, err = s.QueryContext(ctx)
		} else {
			result, err = s.ExecContext(ctx)
		}
	} else if query {
		rows, err = c.QueryContext(ctx, q)
	} else {
		result, err = c.ExecContext(ctx, q)
	}
	if wantError != nil {
		if rows != nil {
			rows.Close()
			t.Fatal("failed setting returned rows")
		}
		timeDiagnostic(t, err, *wantError)
		return
	}
	if err != nil {
		t.Fatalf("%s (prepared=%v): %v", q, prepared, err)
	}
	if !query {
		n, err := result.RowsAffected()
		if err != nil || n != 0 {
			t.Fatal(n, err)
		}
		return
	}
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "TEXT" {
		t.Fatal(types, err)
	}
	want := "<+01>-01"
	if strings.HasPrefix(q, "SELECT (") {
		want = "2004-07-01 11:00:00"
	}
	if !rows.Next() {
		t.Fatal("missing row", rows.Err())
	}
	var got string
	if err := rows.Scan(&got); err != nil || got != want {
		t.Fatal(fmt.Sprintf("%s: got %q, want %q", q, got, want), err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("unexpected extra row", rows.Err())
	}
}
