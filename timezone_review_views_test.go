package stuntgres_test

import (
	"context"
	"database/sql"
	"stuntgres"
	"testing"
)

func TestTimezoneReviewViewsSessionCapture(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	c := timezoneConn(t, db)
	for _, q := range []string{
		"SET TIME ZONE 'UTC'", "SET DateStyle='ISO, MDY'",
		"CREATE TABLE tz_view_source(id INT)",
		"CREATE VIEW tz_view AS SELECT CASE DATE '2004-10-19' WHEN TIMESTAMPTZ '2004-10-19 04:00+00' THEN TRUE ELSE FALSE END AS same,DATE '2003-01-02'::TEXT AS rendered FROM tz_view_source",
		"CREATE VIEW tz_nested WITH(security_invoker=true) AS SELECT same,rendered FROM tz_view",
		"ALTER TABLE tz_view_source RENAME TO tz_view_pinned",
		"CREATE TABLE tz_view_source(id INT)",
	} {
		timezoneExec(t, c, q, 0)
	}
	timezoneExec(t, c, "INSERT INTO tz_view_pinned VALUES(1)", 1)
	timezoneExec(t, c, "INSERT INTO tz_view_source VALUES(2),(3)", 2)
	for _, name := range []string{"tz_view", "tz_nested"} {
		q := "SELECT same,rendered FROM " + name
		stmt, err := c.PrepareContext(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			zone, style, text string
			want              bool
		}{
			{"UTC", "ISO, MDY", "2003-01-02", false},
			{"America/New_York", "SQL, DMY", "02/01/2003", true},
			{"UTC", "German, DMY", "02.01.2003", false},
		} {
			timezoneExec(t, c, "SET TIME ZONE '"+tc.zone+"'", 0)
			timezoneExec(t, c, "SET DateStyle='"+tc.style+"'", 0)
			for _, prepared := range []bool{false, true} {
				var rows *sql.Rows
				if prepared {
					rows, err = stmt.QueryContext(ctx)
				} else {
					rows, err = c.QueryContext(ctx, q)
				}
				queryCoreRows(t, rows, err, []string{"same", "rendered"}, []string{"BOOL", "TEXT"}, [][]any{{tc.want, tc.text}}, false)
			}
		}
		stmt.Close()
	}
}
