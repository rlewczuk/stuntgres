package stuntgres_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres"
)

func TestTimezoneReviewSessionConditionalBehavior(t *testing.T) {
	ctx := context.Background()
	c := timezoneConn(t, openLocal(t, stuntgres.NewDriver(), t.Name()))
	timezoneExec(t, c, "CREATE TABLE tz_review(id INTEGER)", 0)
	timezoneExec(t, c, "INSERT INTO tz_review VALUES(1)", 1)
	const query = "SELECT NULLIF(DATE '2004-10-19',TIMESTAMPTZ '2004-10-19 04:00+00') IS NULL,CASE DATE '2004-10-19' WHEN TIMESTAMPTZ '2004-10-19 04:00+00' THEN TRUE ELSE FALSE END,CASE TIMESTAMPTZ '2004-10-19 04:00+00' WHEN DATE '2004-10-19' THEN TRUE ELSE FALSE END FROM tz_review"
	timezoneExec(t, c, "SET TIME ZONE 'UTC'", 0)
	prepared, err := c.PrepareContext(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	for _, tc := range []struct {
		zone string
		want bool
	}{{"UTC", false}, {"America/New_York", true}, {"UTC", false}} {
		timezoneExec(t, c, "SET TIME ZONE '"+tc.zone+"'", 0)
		timezoneRows(t, c, query, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{tc.want, tc.want, tc.want}})
		rows, err := prepared.QueryContext(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var a, b, d bool
		if !rows.Next() {
			rows.Close()
			t.Fatal("missing prepared conditional row")
		}
		if err := rows.Scan(&a, &b, &d); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if a != tc.want || b != tc.want || d != tc.want || rows.Next() || rows.Err() != nil {
			rows.Close()
			t.Fatalf("prepared zone %s: %t %t %t, want %t", tc.zone, a, b, d, tc.want)
		}
		rows.Close()
	}
}

func TestTimezoneReviewVolatileATBehavior(t *testing.T) {
	ctx := context.Background()
	for _, prepared := range []bool{false, true} {
		for _, suffix := range []string{"AT LOCAL", "AT TIME ZONE 'UTC'"} {
			t.Run(suffix+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				c := timezoneConn(t, openLocal(t, stuntgres.NewDriver(), t.Name()))
				timezoneExec(t, c, "SET TIME ZONE 'UTC'", 0)
				timezoneExec(t, c, "CREATE TABLE tz_review(id INTEGER)", 0)
				timezoneExec(t, c, "INSERT INTO tz_review VALUES(1),(2),(3)", 3)
				timezoneExec(t, c, "CREATE SEQUENCE tz_review_seq", 0)
				query := "SELECT ((CASE WHEN nextval('tz_review_seq')=2 THEN TIMESTAMP '2004-10-19 00:00' ELSE TIMESTAMP 'epoch' END) " + suffix + ")::TEXT FROM tz_review ORDER BY id"
				var stmt *sql.Stmt
				if prepared {
					var err error
					stmt, err = c.PrepareContext(ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
				}
				for run := range 2 {
					var rows *sql.Rows
					var err error
					if stmt == nil {
						rows, err = c.QueryContext(ctx, query)
					} else {
						rows, err = stmt.QueryContext(ctx)
					}
					if err != nil {
						t.Fatal(err)
					}
					for i := range 3 {
						want := "1970-01-01 00:00:00+00"
						if run == 0 && i == 1 {
							want = "2004-10-19 00:00:00+00"
						}
						var got string
						if !rows.Next() {
							rows.Close()
							t.Fatal("missing volatile AT row", i)
						}
						if err := rows.Scan(&got); err != nil || got != want {
							rows.Close()
							t.Fatal(got, want, err)
						}
					}
					if rows.Next() || rows.Err() != nil {
						rows.Close()
						t.Fatal("unexpected volatile AT cardinality or iteration error")
					}
					rows.Close()
					timezoneRows(t, c, "SELECT currval('tz_review_seq')", []string{"INT8"}, [][]any{{int64(3 * (run + 1))}})
				}
			})
		}
	}
}
