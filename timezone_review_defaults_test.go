package stuntgres_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"stuntgres"
)

const timezoneReviewDefaultsDDL = "CREATE TABLE tz_defaults(id INTEGER,z TIMESTAMPTZ DEFAULT (TIMESTAMP '2004-07-01 12:00' AT LOCAL),d TIMESTAMPTZ DEFAULT DATE '2004-10-19'::TIMESTAMPTZ,s TEXT DEFAULT DATE '2004-10-19'::TEXT,b BOOL DEFAULT (DATE '2004-10-19'=TIMESTAMPTZ '2004-10-19 04:00+00'),k BOOL DEFAULT CASE DATE '2004-10-19' WHEN TIMESTAMPTZ '2004-10-19 04:00+00' THEN TRUE ELSE FALSE END,n DATE DEFAULT NULLIF(DATE '2004-10-19',TIMESTAMPTZ '2004-10-19 04:00+00'))"
const timezoneReviewDefaultsQuery = "SELECT id,(z AT TIME ZONE 'UTC')::TEXT,(d AT TIME ZONE 'UTC')::TEXT,s,b,k,n::TEXT FROM tz_defaults ORDER BY id"

func TestTimezoneReviewStableDefaultsExecution(t *testing.T) {
	ctx := context.Background()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			c := timezoneConn(t, openLocal(t, stuntgres.NewDriver(), t.Name()))
			timezoneExec(t, c, "SET TIME ZONE 'UTC'", 0)
			if prepared {
				stmt, err := c.PrepareContext(ctx, timezoneReviewDefaultsDDL)
				if err != nil {
					t.Fatal(err)
				}
				result, err := stmt.ExecContext(ctx)
				stmt.Close()
				if err != nil {
					t.Fatal(err)
				}
				if count, err := result.RowsAffected(); err != nil || count != 0 {
					t.Fatal(count, err)
				}
			} else {
				timezoneExec(t, c, timezoneReviewDefaultsDDL, 0)
			}
			stmt, err := c.PrepareContext(ctx, "INSERT INTO tz_defaults(id) VALUES($1)")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for i, settings := range []struct{ zone, style string }{{"UTC", "ISO,MDY"}, {"America/New_York", "SQL,DMY"}} {
				timezoneExec(t, c, "SET TIME ZONE '"+settings.zone+"'", 0)
				timezoneExec(t, c, "SET DateStyle='"+settings.style+"'", 0)
				if prepared {
					result, err := stmt.ExecContext(ctx, i+1)
					if err != nil {
						t.Fatal(err)
					}
					if count, err := result.RowsAffected(); err != nil || count != 1 {
						t.Fatal(count, err)
					}
				} else {
					timezoneExec(t, c, fmt.Sprintf("INSERT INTO tz_defaults(id) VALUES(%d)", i+1), 1)
				}
			}
			timezoneExec(t, c, "INSERT INTO tz_defaults VALUES(3,DEFAULT,DEFAULT,DEFAULT,DEFAULT,DEFAULT,DEFAULT),(4,NULL,NULL,'explicit',NULL,NULL,NULL)", 2)
			timezoneExec(t, c, "SET DateStyle='ISO,MDY'", 0)
			timezoneRows(t, c, timezoneReviewDefaultsQuery, []string{"INT4", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL", "TEXT"}, [][]any{
				{int64(1), "2004-07-01 12:00:00", "2004-10-19 00:00:00", "2004-10-19", false, false, "2004-10-19"},
				{int64(2), "2004-07-01 16:00:00", "2004-10-19 04:00:00", "19/10/2004", true, true, nil},
				{int64(3), "2004-07-01 16:00:00", "2004-10-19 04:00:00", "19/10/2004", true, true, nil},
				{int64(4), nil, nil, "explicit", nil, nil, nil},
			})
		})
	}
}

func TestTimezoneReviewStableDefaultsReplay(t *testing.T) {
	ctx := context.Background()
	for _, populated := range []bool{false, true} {
		t.Run(fmt.Sprint(populated), func(t *testing.T) {
			driver := stuntgres.NewDriver()
			source := openLocal(t, driver, t.Name())
			dest := openLocal(t, driver, t.Name()+"/dest")
			c, target := timezoneConn(t, source), timezoneConn(t, dest)
			timezoneExec(t, c, "SET TIME ZONE 'America/New_York'", 0)
			timezoneExec(t, c, timezoneReviewDefaultsDDL, 0)
			timezoneExec(t, c, "CREATE TABLE tz_deferred(id INTEGER,z TIMESTAMPTZ DEFAULT (TIMESTAMP '2004-07-01 12:00' AT TIME ZONE 'No/Such_Zone'))", 0)
			timezoneExec(t, c, "INSERT INTO tz_deferred VALUES(1,NULL)", 1)
			if populated {
				timezoneExec(t, c, "INSERT INTO tz_defaults(id) VALUES(1)", 1)
			}
			var dump bytes.Buffer
			if err := stuntgres.Dump(ctx, source, &dump); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(dump.String(), "AT LOCAL") || !strings.Contains(dump.String(), "No/Such_Zone") {
				t.Fatal("Dump lost unevaluated definition", dump.String())
			}
			timezoneExec(t, target, "SET TIME ZONE 'Asia/Tokyo'", 0)
			timezoneExec(t, target, "SET DateStyle='SQL,DMY'", 0)
			timezoneExec(t, target, "CREATE TABLE sentinel(v INTEGER)", 0)
			timezoneExec(t, target, "INSERT INTO sentinel VALUES(7)", 1)
			held, err := target.QueryContext(ctx, "SELECT v FROM sentinel")
			if err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(ctx, dest, bytes.NewReader(dump.Bytes())); err != nil {
				held.Close()
				t.Fatal(err)
			}
			timeCheckRows(t, held, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})
			timezoneRows(t, target, "SHOW TimeZone", []string{"TEXT"}, [][]any{{"Asia/Tokyo"}})
			timezoneRows(t, target, "SHOW DateStyle", []string{"TEXT"}, [][]any{{"SQL, DMY"}})
			timezoneExec(t, target, "INSERT INTO tz_defaults(id) VALUES(2)", 1)
			timezoneExec(t, target, "SET DateStyle='ISO,MDY'", 0)
			want := [][]any{}
			if populated {
				want = append(want, []any{int64(1), "2004-07-01 16:00:00", "2004-10-19 04:00:00", "2004-10-19", true, true, nil})
			}
			want = append(want, []any{int64(2), "2004-07-01 03:00:00", "2004-10-18 15:00:00", "19/10/2004", false, false, "2004-10-19"})
			timezoneRows(t, target, timezoneReviewDefaultsQuery, []string{"INT4", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL", "TEXT"}, want)
			_, err = target.ExecContext(ctx, "INSERT INTO tz_deferred(id) VALUES(2)")
			timeDiagnostic(t, err, stuntgres.Error{Code: "22023", Message: `time zone "No/Such_Zone" not recognized`})
			timezoneRows(t, target, "SELECT id,z::TEXT FROM tz_deferred ORDER BY id", []string{"INT4", "TEXT"}, [][]any{{int64(1), nil}})
		})
	}
}
