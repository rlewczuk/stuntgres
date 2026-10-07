package stuntgres_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"stuntgres"
	"testing"
)

func TestTimezoneDateStyleAndSettingsLifecycle(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(2)
	c, peer := timezoneConn(t, db), timezoneConn(t, db)
	timezoneExec(t, c, "CREATE TABLE tz_witness(v INTEGER)", 0)
	timezoneExec(t, c, "INSERT INTO tz_witness VALUES(1)", 1)
	for _, tc := range []struct{ setting, want string }{{"ISO,MDY", "2003-01-02"}, {"ISO,DMY", "2003-02-01"}, {"ISO,YMD", "2001-02-03"}} {
		timezoneExec(t, c, "SET DateStyle='"+tc.setting+"'", 0)
		timezoneRows(t, c, "SELECT DATE '01/02/03' FROM tz_witness", []string{"DATE"}, [][]any{{tc.want}})
	}
	for _, tc := range []struct{ setting, date, stamp string }{{"ISO,MDY", "1997-12-17", "1997-12-17 07:37:16"}, {"SQL,MDY", "12/17/1997", "12/17/1997 07:37:16"}, {"SQL,DMY", "17/12/1997", "17/12/1997 07:37:16"}, {"German", "17.12.1997", "17.12.1997 07:37:16"}, {"Postgres,MDY", "1997-12-17", "Wed Dec 17 07:37:16 1997"}, {"Postgres,DMY", "1997-12-17", "Wed 17 Dec 07:37:16 1997"}} {
		timezoneExec(t, c, "SET DateStyle='"+tc.setting+"'", 0)
		timezoneRows(t, c, "SELECT DATE '1997-12-17',TIMESTAMP '1997-12-17 07:37:16'::TEXT,TIMETZ '12:00+02' FROM tz_witness", []string{"DATE", "TEXT", "TIMETZ"}, [][]any{{tc.date, tc.stamp, "12:00:00+02"}})
	}
	timezoneExec(t, c, "SET DateStyle='ISO,MDY'", 0)
	timezoneExec(t, c, "SET DateStyle='DMY'", 0)
	timezoneRows(t, c, "SHOW DateStyle", []string{"TEXT"}, [][]any{{"ISO, DMY"}})
	timezoneRows(t, peer, "SHOW DateStyle", []string{"TEXT"}, [][]any{{"ISO, MDY"}})
	for _, tc := range []struct{ sql, show, text string }{{"SET TIME ZONE -7", "<-07>+07", "2004-01-01 05:00:00-07"}, {"SET TIME ZONE 5.5", "<+05:30>-05:30", "2004-01-01 17:30:00+05:30"}, {"SET TIME ZONE 16", "<+16>-16", "2004-01-02 04:00:00+16"}, {"SET TIME ZONE INTERVAL '-08:00' HOUR TO MINUTE", "<-08>+08", "2004-01-01 04:00:00-08"}, {"SET TIME ZONE INTERVAL '90' MINUTE", "<+01:30>-01:30", "2004-01-01 13:30:00+01:30"}, {"SET TIME ZONE 'UTC0:00:60'", "UTC0:00:60", "2004-01-01 11:59:00-00:01"}} {
		timezoneExec(t, c, tc.sql, 0)
		timezoneRows(t, c, "SHOW TIME ZONE", []string{"TEXT"}, [][]any{{tc.show}})
		timezoneRows(t, c, "SELECT TIMESTAMPTZ '2004-01-01 12:00+00'::TEXT FROM tz_witness", []string{"TEXT"}, [][]any{{tc.text}})
	}
	timezoneExec(t, c, "RESET TIME ZONE", 0)
	timezoneRows(t, c, "SELECT current_setting('TimeZone')", []string{"TEXT"}, [][]any{{"UTC"}})
	timezoneRows(t, c, "SELECT set_config('TimeZone','America/New_York',false)", []string{"TEXT"}, [][]any{{"America/New_York"}})
	timezoneExec(t, c, "BEGIN", 0)
	timezoneExec(t, c, "SET LOCAL TIME ZONE 'Asia/Tokyo'", 0)
	timezoneExec(t, c, "SET LOCAL DateStyle='SQL,MDY'", 0)
	timezoneExec(t, c, "SAVEPOINT mark", 0)
	timezoneExec(t, c, "SET TIME ZONE 'UTC'", 0)
	timezoneExec(t, c, "SET DateStyle='German'", 0)
	timezoneExec(t, c, "ROLLBACK TO mark", 0)
	timezoneRows(t, c, "SHOW TimeZone", []string{"TEXT"}, [][]any{{"Asia/Tokyo"}})
	timezoneRows(t, c, "SHOW DateStyle", []string{"TEXT"}, [][]any{{"SQL, MDY"}})
	timezoneExec(t, c, "COMMIT", 0)
	timezoneRows(t, c, "SHOW TimeZone", []string{"TEXT"}, [][]any{{"America/New_York"}})
	timezoneRows(t, c, "SHOW DateStyle", []string{"TEXT"}, [][]any{{"ISO, DMY"}})
	timezoneExec(t, c, "BEGIN", 0)
	timezoneExec(t, c, "SET TIME ZONE 'Asia/Tokyo'", 0)
	timezoneExec(t, c, "SET DateStyle='German'", 0)
	timezoneExec(t, c, "ROLLBACK", 0)
	timezoneRows(t, c, "SHOW TimeZone", []string{"TEXT"}, [][]any{{"America/New_York"}})
	timezoneRows(t, peer, "SHOW TimeZone", []string{"TEXT"}, [][]any{{"UTC"}})
	timezoneExec(t, c, "RESET ALL", 0)
	timezoneRows(t, c, "SHOW DateStyle", []string{"TEXT"}, [][]any{{"ISO, MDY"}})
	// Returning one handle while holding its peer makes reborrow deterministic.
	timezoneExec(t, c, "SET TIME ZONE 'Asia/Tokyo'", 0)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	timezoneRows(t, again, "SHOW TimeZone", []string{"TEXT"}, [][]any{{"Asia/Tokyo"}})
}

func TestTimezoneSessionAbbreviationPriority(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	c := timezoneConn(t, db)
	timezoneExec(t, c, "CREATE TABLE tz_witness(v INTEGER)", 0)
	timezoneExec(t, c, "INSERT INTO tz_witness VALUES(1)", 1)
	for _, tc := range []struct{ zone, input, want string }{{"Asia/Shanghai", "2000-01-01 12:00 CST", "2000-01-01 04:00:00"}, {"UTC", "2000-01-01 12:00 CST", "2000-01-01 18:00:00"}, {"Asia/Kolkata", "2000-01-01 12:00 IST", "2000-01-01 06:30:00"}, {"UTC", "2000-01-01 12:00 IST", "2000-01-01 10:00:00"}, {"America/New_York", "2000-01-01 12:00 LMT", "2000-01-01 16:56:02"}} {
		timezoneExec(t, c, "SET TIME ZONE '"+tc.zone+"'", 0)
		timezoneRows(t, c, "SELECT (TIMESTAMPTZ '"+tc.input+"' AT TIME ZONE 'UTC')::TEXT FROM tz_witness", []string{"TEXT"}, [][]any{{tc.want}})
	}
}

func TestTimezonePhysicalReplacementAndDefaultAliases(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(1)
	c := timezoneConn(t, db)
	timezoneExec(t, c, "CREATE TABLE tz_witness(z TIMESTAMPTZ)", 0)
	timezoneExec(t, c, "INSERT INTO tz_witness VALUES('2004-01-01 12:00+00')", 1)
	timezoneExec(t, c, "SET TIME ZONE 'Asia/Tokyo'", 0)
	timezoneExec(t, c, "SET TIME ZONE LOCAL", 0)
	timezoneRows(t, c, "SHOW TIME ZONE", []string{"TEXT"}, [][]any{{"UTC"}})
	timezoneExec(t, c, "SET TIME ZONE 'Asia/Tokyo'", 0)
	timezoneExec(t, c, "SET TIME ZONE DEFAULT", 0)
	timezoneRows(t, c, "SHOW timezone", []string{"TEXT"}, [][]any{{"UTC"}})
	timezoneExec(t, c, "SET DateStyle='German'", 0)
	timezoneRows(t, c, "SHOW DateStyle", []string{"TEXT"}, [][]any{{"German, DMY"}})
	timezoneExec(t, c, "SET DateStyle='DEFAULT,DMY'", 0)
	timezoneRows(t, c, "SHOW DateStyle", []string{"TEXT"}, [][]any{{"ISO, DMY"}})
	timezoneExec(t, c, "SET TIME ZONE 'Asia/Tokyo'", 0)
	if err := c.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
		t.Fatal(err)
	}
	c.Close()
	fresh, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	timezoneRows(t, fresh, "SHOW TimeZone", []string{"TEXT"}, [][]any{{"UTC"}})
	timezoneRows(t, fresh, "SHOW DateStyle", []string{"TEXT"}, [][]any{{"ISO, MDY"}})
	timezoneRows(t, fresh, "SELECT z::TEXT FROM tz_witness", []string{"TEXT"}, [][]any{{"2004-01-01 12:00:00+00"}})
}

func TestTimezoneBuiltinDescriptionsAndZonedDateStyle(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	c := timezoneConn(t, db)
	timezoneExec(t, c, "CREATE TABLE tz_witness(v INTEGER)", 0)
	timezoneExec(t, c, "INSERT INTO tz_witness VALUES(1)", 1)
	rows, err := c.QueryContext(context.Background(), "SHOW ALL")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var name, value, description string
		if err = rows.Scan(&name, &value, &description); err != nil {
			t.Fatal(err)
		}
		switch name {
		case "TimeZone":
			if value != "UTC" || description != "Sets the time zone for displaying and interpreting time stamps." {
				t.Fatal(name, value, description)
			}
			seen[name] = true
		case "DateStyle":
			if value != "ISO, MDY" || description != "Sets the display format for date and time values." {
				t.Fatal(name, value, description)
			}
			seen[name] = true
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if !seen["TimeZone"] || !seen["DateStyle"] {
		t.Fatal("missing builtins", seen)
	}
	for _, tc := range []struct{ style, want string }{{"SQL,MDY", "12/17/1997 07:37:16 UTC"}, {"SQL,DMY", "17/12/1997 07:37:16 UTC"}, {"German", "17.12.1997 07:37:16 UTC"}, {"Postgres,MDY", "Wed Dec 17 07:37:16 1997 UTC"}, {"Postgres,DMY", "Wed 17 Dec 07:37:16 1997 UTC"}} {
		timezoneExec(t, c, "SET DateStyle='"+tc.style+"'", 0)
		timezoneRows(t, c, "SELECT TIMESTAMPTZ '1997-12-17 07:37:16+00'::TEXT FROM tz_witness", []string{"TEXT"}, [][]any{{tc.want}})
	}
}
