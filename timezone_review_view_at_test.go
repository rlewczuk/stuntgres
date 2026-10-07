package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
)

const timezoneReviewViewAtDDL = "CREATE VIEW tz_at AS SELECT id,value AT TIME ZONE zone AS explicit,value AT LOCAL AS local_value,TIMESTAMP(3) '2004-07-01 12:00:00.1234' AT TIME ZONE 'UTC' AS frozen FROM tz_at_source"
const timezoneReviewViewAtQuery = "SELECT id,(explicit AT TIME ZONE 'UTC')::TEXT AS explicit,(local_value AT TIME ZONE 'UTC')::TEXT AS local_value,(frozen AT TIME ZONE 'UTC')::TEXT AS frozen FROM tz_at_nested ORDER BY id"

func TestTimezoneReviewViewAtExecution(t *testing.T) {
	ctx := context.Background()
	c := timezoneConn(t, openLocal(t, stuntgres.NewDriver(), t.Name()))
	for _, q := range []string{
		"SET TIME ZONE 'UTC'", "CREATE TABLE tz_at_source(id INT,value TIMESTAMP(3),zone TEXT)",
	} {
		timezoneExec(t, c, q, 0)
	}
	create, err := c.PrepareContext(ctx, timezoneReviewViewAtDDL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = create.ExecContext(ctx)
	create.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		"CREATE VIEW tz_at_nested WITH(security_invoker=true) AS SELECT * FROM tz_at",
		"ALTER TABLE tz_at_source RENAME TO tz_at_pinned",
		"ALTER TABLE tz_at_pinned RENAME COLUMN value TO civil",
		"CREATE TABLE tz_at_source(id INT,value TIMESTAMP(3),zone TEXT)",
	} {
		timezoneExec(t, c, q, 0)
	}
	timezoneExec(t, c, "INSERT INTO tz_at_pinned VALUES(1,'2004-07-01 12:00:00.1234','America/New_York'),(2,NULL,'UTC'),(3,'2004-07-01 12:00',NULL)", 3)
	timezoneExec(t, c, "INSERT INTO tz_at_source VALUES(99,'2000-01-01','UTC')", 1)
	timezoneRows(t, c, "SELECT explicit,local_value FROM tz_at WHERE id=2", []string{"TIMESTAMPTZ", "TIMESTAMPTZ"}, [][]any{{nil, nil}})
	stmt, err := c.PrepareContext(ctx, timezoneReviewViewAtQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, tc := range []struct{ zone, local string }{{"UTC", "2004-07-01 12:00:00.123"}, {"America/New_York", "2004-07-01 16:00:00.123"}, {"Asia/Tokyo", "2004-07-01 03:00:00.123"}} {
		timezoneExec(t, c, "SET TIME ZONE '"+tc.zone+"'", 0)
		for _, prepared := range []bool{false, true} {
			var rows *sql.Rows
			if prepared {
				rows, err = stmt.QueryContext(ctx)
			} else {
				rows, err = c.QueryContext(ctx, timezoneReviewViewAtQuery)
			}
			queryCoreRows(t, rows, err, []string{"id", "explicit", "local_value", "frozen"}, []string{"INT4", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(1), "2004-07-01 16:00:00.123", tc.local, "2004-07-01 12:00:00.123"}, {int64(2), nil, nil, "2004-07-01 12:00:00.123"}, {int64(3), nil, strings.TrimSuffix(tc.local, ".123"), "2004-07-01 12:00:00.123"}}, false)
		}
	}
}

func TestTimezoneReviewViewAtReplay(t *testing.T) {
	ctx := context.Background()
	driver := stuntgres.NewDriver()
	source, dest := openLocal(t, driver, t.Name()), openLocal(t, driver, t.Name()+"/dest")
	c, target := timezoneConn(t, source), timezoneConn(t, dest)
	for _, q := range []string{"SET TIME ZONE 'America/New_York'", "CREATE TABLE tz_at_source(id INT,value TIMESTAMP(3),zone TEXT)", timezoneReviewViewAtDDL, "CREATE VIEW tz_at_nested AS SELECT * FROM tz_at", "ALTER TABLE tz_at_source RENAME TO tz_at_pinned", "ALTER TABLE tz_at_pinned RENAME COLUMN value TO civil"} {
		timezoneExec(t, c, q, 0)
	}
	timezoneExec(t, c, "INSERT INTO tz_at_pinned VALUES(1,'2004-07-01 12:00:00.1234','America/New_York'),(2,NULL,'UTC')", 2)
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, source, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), "AT LOCAL") || !strings.Contains(dump.String(), "AT TIME ZONE") {
		t.Fatal("lost timezone definition", dump.String())
	}
	timezoneExec(t, target, "SET TIME ZONE 'Asia/Tokyo'", 0)
	if err := stuntgres.Restore(ctx, dest, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	timezoneRows(t, target, "SHOW TimeZone", []string{"TEXT"}, [][]any{{"Asia/Tokyo"}})
	timezoneRows(t, target, timezoneReviewViewAtQuery, []string{"INT4", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(1), "2004-07-01 16:00:00.123", "2004-07-01 03:00:00.123", "2004-07-01 12:00:00.123"}, {int64(2), nil, nil, "2004-07-01 12:00:00.123"}})
	var redump bytes.Buffer
	if err := stuntgres.Dump(ctx, dest, &redump); err != nil {
		t.Fatal(err)
	}
	if dump.String() != redump.String() {
		t.Fatal("view replay changed canonical definition", dump.String(), redump.String())
	}
}
