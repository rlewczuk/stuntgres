package stuntgres_test

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
	"time"
)

type timezoneQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func timezoneRows(t *testing.T, c timezoneQueryer, q string, types []string, want [][]any, args ...any) {
	t.Helper()
	r, err := c.QueryContext(context.Background(), q, args...)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	defer r.Close()
	meta, err := r.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(meta) != len(types) {
		t.Fatalf("%s metadata %v", q, meta)
	}
	for i, typ := range types {
		if meta[i].DatabaseTypeName() != typ {
			t.Fatalf("%s type[%d]=%s want %s", q, i, meta[i].DatabaseTypeName(), typ)
		}
	}
	got := make([][]any, 0)
	for r.Next() {
		row := make([]any, len(types))
		dst := make([]any, len(types))
		for i := range row {
			dst[i] = &row[i]
		}
		if err = r.Scan(dst...); err != nil {
			t.Fatal(err)
		}
		got = append(got, row)
	}
	if err = r.Err(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(q, " ORDER BY ") {
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: %#v want %#v", q, got, want)
		}
		return
	}
	// Match unordered results as bags, including every duplicate occurrence.
	if len(got) != len(want) {
		t.Fatalf("%s cardinality %d want %d: %#v", q, len(got), len(want), got)
	}
	used := make([]bool, len(want))
	for _, row := range got {
		found := false
		for i, expected := range want {
			if !used[i] && reflect.DeepEqual(row, expected) {
				used[i] = true
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%s bag occurrence %#v: %#v want %#v", q, row, got, want)
		}
	}
}
func timezoneExec(t *testing.T, c *sql.Conn, q string, n int64, args ...any) {
	t.Helper()
	r, err := c.ExecContext(context.Background(), q, args...)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	got, err := r.RowsAffected()
	if err != nil || got != n {
		t.Fatalf("%s count %d %v want %d", q, got, err, n)
	}
}
func timezoneConn(t *testing.T, db *sql.DB) *sql.Conn {
	t.Helper()
	c, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestTimezonePooledInstantAndDST(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(2)
	a, b := timezoneConn(t, db), timezoneConn(t, db)
	timezoneExec(t, a, "CREATE TABLE tz_witness(id INTEGER,z TIMESTAMPTZ(3),q TIME(3) WITH TIME ZONE,d DATE,c TIMESTAMP)", 0)
	timezoneExec(t, a, "INSERT INTO tz_witness VALUES(1,'2004-10-19 10:23:54+02','05:34:17-05','2004-10-19','2004-10-19 00:00')", 1)
	for _, tc := range []struct {
		c                 *sql.Conn
		zone, style, text string
	}{{a, "UTC", "ISO, MDY", "2004-10-19 08:23:54+00"}, {b, "America/New_York", "ISO, DMY", "2004-10-19 04:23:54-04"}} {
		timezoneExec(t, tc.c, "SET TIME ZONE '"+tc.zone+"'", 0)
		timezoneExec(t, tc.c, "SET DateStyle='"+tc.style+"'", 0)
		for range 2 {
			timezoneRows(t, tc.c, "SELECT z::TEXT,z=TIMESTAMPTZ '2004-10-19 08:23:54+00',q::TEXT FROM tz_witness", []string{"TEXT", "BOOL", "TEXT"}, [][]any{{tc.text, true, "05:34:17-05"}})
		}
		s, err := tc.c.PrepareContext(context.Background(), "SELECT z::TEXT FROM tz_witness")
		if err != nil {
			t.Fatal(err)
		}
		var got string
		err = s.QueryRowContext(context.Background()).Scan(&got)
		s.Close()
		if err != nil || got != tc.text {
			t.Fatal(got, err)
		}
		timezoneRows(t, tc.c, "SHOW DateStyle", []string{"TEXT"}, [][]any{{tc.style}})
	}
	for _, tc := range []struct{ input, want string }{
		{"2018-03-11 01:30 America/New_York", "2018-03-11 06:30:00+00"}, {"2018-03-11 02:30 America/New_York", "2018-03-11 07:30:00+00"}, {"2018-03-11 03:30 America/New_York", "2018-03-11 07:30:00+00"},
		{"2018-11-04 01:30 America/New_York", "2018-11-04 06:30:00+00"}, {"2018-11-04 01:30 EDT", "2018-11-04 05:30:00+00"},
		{"2020-10-04 02:15 Australia/Lord_Howe", "2020-10-03 15:45:00+00"}, {"2021-04-04 01:45 Australia/Lord_Howe", "2021-04-03 15:15:00+00"},
		{"2004-07-01 12:00 EST", "2004-07-01 17:00:00+00"}, {"2004-07-01 12:00 EDT", "2004-07-01 16:00:00+00"}, {"2004-07-01 12:00 aMeRiCa/NeW_YoRk", "2004-07-01 16:00:00+00"},
		{"2004-01-01 12:00-05:30:15", "2004-01-01 17:30:15+00"}, {"1800-01-01 12:00 Europe/Amsterdam", "1800-01-01 11:40:28+00"},
		{"2010-07-01 12:00 MSK", "2010-07-01 09:00:00+00"}, {"2012-07-01 12:00 MSK", "2012-07-01 08:00:00+00"}, {"2015-07-01 12:00 MSK", "2015-07-01 09:00:00+00"},
	} {
		timezoneRows(t, a, "SELECT TIMESTAMPTZ '"+tc.input+"'::TEXT FROM tz_witness", []string{"TEXT"}, [][]any{{tc.want}})
	}
	timezoneRows(t, b, "SELECT TIMESTAMPTZ '2004-07-01 12:00' AT TIME ZONE 'UTC' FROM tz_witness", []string{"TIMESTAMP"}, [][]any{{time.Date(2004, 7, 1, 16, 0, 0, 0, time.UTC)}})
	timezoneRows(t, a, "SELECT TIMESTAMPTZ(3) '2004-01-01 00:00:00.9995+00'::TEXT,TIMESTAMP '2004-01-01 12:00+02'::TEXT FROM tz_witness", []string{"TEXT", "TEXT"}, [][]any{{"2004-01-01 00:00:01+00", "2004-01-01 12:00:00"}})
}

func TestTimezoneConversionComparisonAndComposition(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	c := timezoneConn(t, db)
	timezoneExec(t, c, "CREATE TABLE tz_witness(z TIMESTAMPTZ,q TIMETZ)", 0)
	timezoneExec(t, c, "INSERT INTO tz_witness VALUES('2004-10-19 04:00+00','09:00+01'),('2004-10-19 04:00+00','08:00+00'),(NULL,NULL)", 3)
	timezoneExec(t, c, "SET TIME ZONE 'America/New_York'", 0)
	timezoneRows(t, c, "SELECT DATE '2004-10-19'=z,z=TIMESTAMP '2004-10-19 00:00',DATE '2004-10-19' BETWEEN z AND z,z IS DISTINCT FROM NULL FROM tz_witness WHERE z IS NOT NULL", []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}, {true, true, true, true}})
	timezoneRows(t, c, "SELECT z::TEXT,count(*) FROM tz_witness WHERE z IS NOT NULL GROUP BY z", []string{"TEXT", "INT8"}, [][]any{{"2004-10-19 00:00:00-04", int64(2)}})
	timezoneRows(t, c, "SELECT q::TEXT FROM tz_witness WHERE q IS NOT NULL ORDER BY tz_witness.q", []string{"TEXT"}, [][]any{{"09:00:00+01"}, {"08:00:00+00"}})
	timezoneRows(t, c, "SELECT TIMETZ '09:00+01'=TIMETZ '08:00+00',TIMETZ '09:00+01'<TIMETZ '08:00+00',TIMETZ '05:34:17-05' AT TIME ZONE 'UTC',TIMETZ '23:30+00' AT TIME ZONE 'UTC-2' FROM tz_witness WHERE q=TIMETZ '09:00+01'", []string{"BOOL", "BOOL", "TIMETZ", "TIMETZ"}, [][]any{{false, true, "10:34:17+00", "01:30:00+02"}})
	timezoneRows(t, c, "SELECT (TIMESTAMP '2004-07-01 12:00' AT TIME ZONE 'America/New_York') AT TIME ZONE 'UTC',(TIMESTAMP '2004-07-01 12:00' AT LOCAL)::TEXT,(NULL::TIMESTAMPTZ AT LOCAL),CASE WHEN TRUE THEN 'safe' ELSE (TIMESTAMP '2004-07-01' AT TIME ZONE 'No/Such_Zone')::TEXT END FROM tz_witness WHERE q=TIMETZ '09:00+01'", []string{"TIMESTAMP", "TEXT", "TIMESTAMP", "TEXT"}, [][]any{{time.Date(2004, 7, 1, 16, 0, 0, 0, time.UTC), "2004-07-01 12:00:00-04", nil, "safe"}})
	timezoneExec(t, c, "SET TIME ZONE 'UTC'", 0)
	timezoneRows(t, c, "SELECT DATE '2004-10-19'=z,z=TIMESTAMP '2004-10-19 00:00' FROM tz_witness WHERE q=TIMETZ '09:00+01'", []string{"BOOL", "BOOL"}, [][]any{{false, false}})
}

func TestTimezoneParametersMetadataAndPreparedFreeze(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	c := timezoneConn(t, db)
	ctx := context.Background()
	timezoneExec(t, c, "CREATE TABLE tz_witness(id INTEGER,z TIMESTAMP(3) WITH TIME ZONE,q TIMETZ(3))", 0)
	timezoneExec(t, c, "SET TIME ZONE 'America/New_York'", 0)
	frozen, err := c.PrepareContext(ctx, "SELECT TIMESTAMPTZ '2004-07-01 12:00'::TEXT,TIMETZ '05:34:17-05' FROM tz_witness WHERE id=1")
	if err != nil {
		t.Fatal(err)
	}
	defer frozen.Close()
	p, err := c.PrepareContext(ctx, "INSERT INTO tz_witness VALUES($1,$2,$3)")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for _, args := range [][]any{{1, "2004-07-01 12:00", "05:34:17-05"}, {2, time.Date(2004, 7, 1, 18, 0, 0, 123500000, time.FixedZone("east", 7200)), nil}, {3, nil, nil}} {
		r, err := p.ExecContext(ctx, args...)
		if err != nil {
			t.Fatal(err)
		}
		if n, err := r.RowsAffected(); err != nil || n != 1 {
			t.Fatal(n, err)
		}
	}
	r, err := c.QueryContext(ctx, "SELECT z,q FROM tz_witness WHERE id=3")
	if err != nil {
		t.Fatal(err)
	}
	m, err := r.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 2 {
		t.Fatalf("metadata columns %d want 2", len(m))
	}
	for i, typ := range []string{"TIMESTAMPTZ", "TIMETZ"} {
		nullable, known := m[i].Nullable()
		precision, scale, decimal := m[i].DecimalSize()
		scan := reflect.TypeFor[string]()
		if i == 0 {
			scan = reflect.TypeFor[time.Time]()
		}
		if m[i].DatabaseTypeName() != typ || m[i].ScanType() != scan || !known || !nullable || decimal || precision != 0 || scale != 0 {
			t.Fatalf("metadata %s %#v decimal %d,%d,%v", typ, m[i], precision, scale, decimal)
		}
	}
	if !r.Next() {
		t.Fatal("missing NULL metadata witness", r.Err())
	}
	var nullZ, nullQ any
	if err = r.Scan(&nullZ, &nullQ); err != nil || nullZ != nil || nullQ != nil {
		t.Fatal(nullZ, nullQ, err)
	}
	if r.Next() || r.Err() != nil {
		t.Fatal("unexpected NULL witness cardinality", r.Err())
	}
	r.Close()
	timezoneExec(t, c, "SET TIME ZONE 'UTC'", 0)
	var z, q string
	if err = frozen.QueryRowContext(ctx).Scan(&z, &q); err != nil || z != "2004-07-01 16:00:00+00" || q != "05:34:17-05" {
		t.Fatal(z, q, err)
	}
	timezoneRows(t, c, "SELECT z::TEXT,q FROM tz_witness ORDER BY id", []string{"TEXT", "TIMETZ"}, [][]any{{"2004-07-01 16:00:00+00", "05:34:17-05"}, {"2004-07-01 16:00:00.124+00", nil}, {nil, nil}})
	parse, err := c.PrepareContext(ctx, "SELECT $1::TIMESTAMPTZ::TEXT FROM tz_witness WHERE id=1")
	if err != nil {
		t.Fatal(err)
	}
	defer parse.Close()
	for _, tc := range []struct{ zone, want string }{{"UTC", "2004-07-01 12:00:00+00"}, {"America/New_York", "2004-07-01 12:00:00-04"}} {
		timezoneExec(t, c, "SET TIME ZONE '"+tc.zone+"'", 0)
		if err = parse.QueryRowContext(ctx, "2004-07-01 12:00").Scan(&z); err != nil || z != tc.want {
			t.Fatal(z, err)
		}
	}
}
