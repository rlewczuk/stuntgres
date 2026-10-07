package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"reflect"
	"stuntgres"
	"testing"
	"time"
)

// These expectations retain the civil clock and subtract the complete east
// offset for UTC conversion, not just its whole-minute component.
var timezoneParameterOffsetCases = []struct {
	name                 string
	east                 int
	text, truncated, utc string
}{
	{"positive_seconds", 30, "05:34:17.123456+00:00:30", "05:34:17.123456+00", "05:33:47.123456+00"},
	{"negative_seconds", -30, "05:34:17.123456-00:00:30", "05:34:17.123456+00", "05:34:47.123456+00"},
	{"positive_components", 7384, "05:34:17.123456+02:03:04", "05:34:17.123456+02:03", "03:31:13.123456+00"},
	{"negative_components", -7384, "05:34:17.123456-02:03:04", "05:34:17.123456-02:03", "07:37:21.123456+00"},
}

func TestTimezoneReviewParameterOffset(t *testing.T) {
	ctx := context.Background()
	c := timezoneConn(t, openLocal(t, stuntgres.NewDriver(), t.Name()))
	timezoneExec(t, c, "CREATE TABLE tz_parameter_source(id INTEGER)", 0)
	timezoneExec(t, c, "INSERT INTO tz_parameter_source VALUES(1)", 1)
	query := "SELECT $1::TIMETZ AS q,($1::TIMETZ)::TEXT AS text,($1::TIMETZ AT TIME ZONE 'UTC')::TEXT AS utc,$1::TIMETZ=$2::TIMETZ AS equal,$1::TIMETZ<$2::TIMETZ AS less,$1::TIMETZ=$3::TIMETZ AS exact FROM tz_parameter_source"
	stmt, err := c.PrepareContext(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, zone := range []string{"UTC", "Asia/Tokyo"} {
		for _, tc := range timezoneParameterOffsetCases {
			for _, prepared := range []bool{false, true} {
				name := zone + "/" + tc.name + "/direct"
				if prepared {
					name = zone + "/" + tc.name + "/prepared"
				}
				t.Run(name, func(t *testing.T) {
					timezoneExec(t, c, "SET TIME ZONE '"+zone+"'", 0)
					value := time.Date(2004, 7, 1, 5, 34, 17, 123456000, time.FixedZone("source", tc.east))
					var rows *sql.Rows
					var err error
					if prepared {
						rows, err = stmt.QueryContext(ctx, value, tc.truncated, tc.text)
					} else {
						rows, err = c.QueryContext(ctx, query, value, tc.truncated, tc.text)
					}
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					meta, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					if len(meta) != 6 || meta[0].ScanType() != reflect.TypeOf("") {
						t.Fatal("TIMETZ metadata", meta)
					}
					// Fixed-offset output does not reread the session after admission.
					timezoneExec(t, c, "SET TIME ZONE 'America/New_York'", 0)
					timeCheckRows(t, rows, []string{"q", "text", "utc", "equal", "less", "exact"}, []string{"TIMETZ", "TEXT", "TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{tc.text, tc.text, tc.utc, false, tc.east > 0, true}})
				})
			}
		}
	}
}

func TestTimezoneReviewParameterOffsetReplay(t *testing.T) {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	db, dest := openLocal(t, d, t.Name()), openLocal(t, d, t.Name()+"/target")
	c, target := timezoneConn(t, db), timezoneConn(t, dest)
	timezoneExec(t, c, "CREATE TABLE tz_parameter_import(id INTEGER,q TIMETZ(6))", 0)
	stmt, err := c.PrepareContext(ctx, "INSERT INTO tz_parameter_import VALUES($1,$2)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	want := make([][]any, 0, 8)
	for i, tc := range timezoneParameterOffsetCases {
		value := time.Date(2004, 7, 1, 5, 34, 17, 123456000, time.FixedZone("source", tc.east))
		for mode := range 2 {
			id := int64(2*i + mode + 1)
			if mode == 0 {
				timezoneExec(t, c, "INSERT INTO tz_parameter_import VALUES($1,$2)", 1, id, value)
			} else {
				result, err := stmt.ExecContext(ctx, id, value)
				if err != nil {
					t.Fatal(err)
				}
				if n, err := result.RowsAffected(); err != nil || n != 1 {
					t.Fatal(n, err)
				}
			}
			want = append(want, []any{id, tc.text, tc.utc})
		}
	}
	query := "SELECT id,q,(q AT TIME ZONE 'UTC')::TEXT AS utc FROM tz_parameter_import ORDER BY id"
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	timezoneExec(t, target, "SET TIME ZONE 'Asia/Tokyo'", 0)
	timezoneExec(t, target, "SET DateStyle='German, DMY'", 0)
	if err := stuntgres.Restore(ctx, dest, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	timezoneRows(t, target, "SHOW TimeZone", []string{"TEXT"}, [][]any{{"Asia/Tokyo"}})
	timezoneRows(t, target, "SHOW DateStyle", []string{"TEXT"}, [][]any{{"German, DMY"}})
	timezoneRows(t, target, "SELECT id FROM tz_parameter_import ORDER BY tz_parameter_import.q,id", []string{"INT4"}, [][]any{{int64(5)}, {int64(6)}, {int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(7)}, {int64(8)}})
	timezoneRows(t, target, query, []string{"INT4", "TIMETZ", "TEXT"}, want)
	timezoneRows(t, c, query, []string{"INT4", "TIMETZ", "TEXT"}, want)
	var again bytes.Buffer
	if err := stuntgres.Dump(ctx, dest, &again); err != nil || again.String() != dump.String() {
		t.Fatal("redump mismatch", err)
	}
}
