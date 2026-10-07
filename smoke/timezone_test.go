package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"stuntgres"
	"testing"
	"time"
)

func TestTimezoneSmoke(t *testing.T) {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	connector, err := d.OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	db.SetMaxOpenConns(2)
	a, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	exec := func(c *sql.Conn, q string, n int64, args ...any) {
		t.Helper()
		r, err := c.ExecContext(ctx, q, args...)
		if err != nil {
			t.Fatal(q, err)
		}
		got, err := r.RowsAffected()
		if err != nil || got != n {
			t.Fatal(q, got, err)
		}
	}
	check := func(c *sql.Conn, q string, types []string, want []any, args ...any) {
		t.Helper()
		r, err := c.QueryContext(ctx, q, args...)
		if err != nil {
			t.Fatal(q, err)
		}
		defer r.Close()
		m, err := r.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if len(m) != len(types) {
			t.Fatal(m)
		}
		for i, typ := range types {
			if m[i].DatabaseTypeName() != typ {
				t.Fatal(q, m[i].DatabaseTypeName(), typ)
			}
		}
		if !r.Next() {
			t.Fatal(q, r.Err())
		}
		got := make([]any, len(types))
		dst := make([]any, len(types))
		for i := range dst {
			dst[i] = &got[i]
		}
		if err = r.Scan(dst...); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatal(q, got, want)
		}
		if r.Next() || r.Err() != nil {
			t.Fatal("extra row", q, r.Err())
		}
	}
	exec(a, "CREATE TABLE tz_witness(id INTEGER,z TIMESTAMPTZ(3),q TIME(3) WITH TIME ZONE)", 0)
	exec(a, "INSERT INTO tz_witness VALUES(1,'2004-10-19 10:23:54+02','05:34:17-05')", 1)
	exec(a, "SET TIME ZONE 'UTC'", 0)
	exec(b, "SET TIME ZONE 'America/New_York'", 0)
	check(a, "SELECT z::TEXT,z=TIMESTAMPTZ '2004-10-19 08:23:54+00' FROM tz_witness", []string{"TEXT", "BOOL"}, []any{"2004-10-19 08:23:54+00", true})
	check(b, "SELECT z::TEXT,z=TIMESTAMPTZ '2004-10-19 08:23:54+00' FROM tz_witness", []string{"TEXT", "BOOL"}, []any{"2004-10-19 04:23:54-04", true})
	check(a, "SELECT TIMESTAMPTZ '2018-03-11 02:30 America/New_York'::TEXT,TIMESTAMPTZ '2018-11-04 01:30 America/New_York'::TEXT,TIMESTAMPTZ(3) '2004-01-01 00:00:00.9995+00'::TEXT FROM tz_witness", []string{"TEXT", "TEXT", "TEXT"}, []any{"2018-03-11 07:30:00+00", "2018-11-04 06:30:00+00", "2004-01-01 00:00:01+00"})
	check(b, "SELECT (TIMESTAMPTZ '2004-07-01 12:00' AT TIME ZONE 'UTC')::TEXT,DATE '2004-10-19'=TIMESTAMPTZ '2004-10-19 04:00+00',TIMETZ '05:34:17-05' AT TIME ZONE 'UTC' FROM tz_witness", []string{"TEXT", "BOOL", "TIMETZ"}, []any{"2004-07-01 16:00:00", true, "10:34:17+00"})
	exec(b, "SET DateStyle='ISO,DMY'", 0)
	check(b, "SELECT DATE '01/02/03' FROM tz_witness", []string{"DATE"}, []any{"2003-02-01"})
	exec(b, "SET DateStyle='SQL'", 0)
	check(b, "SELECT DATE '1997-12-17' FROM tz_witness", []string{"DATE"}, []any{"17/12/1997"})
	check(a, "SHOW DateStyle", []string{"TEXT"}, []any{"ISO, MDY"})
	exec(a, "SET TIME ZONE 5.5", 0)
	check(a, "SELECT z::TEXT FROM tz_witness", []string{"TEXT"}, []any{"2004-10-19 13:53:54+05:30"})
	exec(a, "SET TIME ZONE INTERVAL '-08:00' HOUR TO MINUTE", 0)
	check(a, "SELECT q FROM tz_witness", []string{"TIMETZ"}, []any{"05:34:17-05"})
	exec(a, "RESET TIME ZONE", 0)
	exec(b, "BEGIN", 0)
	exec(b, "SET LOCAL TIME ZONE 'Asia/Tokyo'", 0)
	exec(b, "COMMIT", 0)
	check(b, "SHOW TimeZone", []string{"TEXT"}, []any{"America/New_York"})
	p, err := a.PrepareContext(ctx, "INSERT INTO tz_witness VALUES($1,$2,$3)")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	r, err := p.ExecContext(ctx, 2, time.Date(2004, 7, 1, 18, 0, 0, 0, time.FixedZone("east", 7200)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := r.RowsAffected(); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	check(a, "SELECT z::TEXT,q FROM tz_witness WHERE id=2", []string{"TEXT", "TIMETZ"}, []any{"2004-07-01 16:00:00+00", nil})
	meta, err := a.QueryContext(ctx, "SELECT NULL::TIMESTAMPTZ,NULL::TIMETZ FROM tz_witness WHERE id=1")
	if err != nil {
		t.Fatal(err)
	}
	m, err := meta.ColumnTypes()
	meta.Close()
	if err != nil || m[0].ScanType() != reflect.TypeFor[time.Time]() || m[1].ScanType() != reflect.TypeFor[string]() {
		t.Fatal(m, err)
	}
	for _, tc := range []struct {
		q    string
		want stuntgres.Error
	}{{"SET TIME ZONE 'No/Such_Zone'", stuntgres.Error{Code: "22023", Message: "invalid value for parameter \"TimeZone\": \"No/Such_Zone\""}}, {"SELECT TIMESTAMPTZ '2004-10-19 10:00+16' FROM tz_witness", stuntgres.Error{Code: "22009", Message: "time zone displacement out of range: \"2004-10-19 10:00+16\"", Position: "19"}}, {"SET DateStyle='ISO,SQL'", stuntgres.Error{Code: "22023", Message: "invalid value for parameter \"DateStyle\": \"ISO,SQL\"", Detail: "Conflicting \"DateStyle\" specifications."}}} {
		_, err = a.ExecContext(ctx, tc.q)
		if err = typeCoreDiagnostic(err, tc.want); err != nil {
			t.Fatal(err)
		}
	}
	check(a, "SHOW TimeZone", []string{"TEXT"}, []any{"UTC"})
	check(a, "SHOW DateStyle", []string{"TEXT"}, []any{"ISO, MDY"})
	if err = b.Close(); err != nil {
		t.Fatal(err)
	}
	var dump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	targetConnector, err := d.OpenConnector(t.Name() + "/restore")
	if err != nil {
		t.Fatal(err)
	}
	target := sql.OpenDB(targetConnector)
	defer target.Close()
	c, err := target.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	exec(c, "SET TIME ZONE 'UTC'", 0)
	if err = stuntgres.Restore(ctx, target, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	check(c, "SELECT z::TEXT,q FROM tz_witness WHERE id=1", []string{"TEXT", "TIMETZ"}, []any{"2004-10-19 08:23:54+00", "05:34:17-05"})
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err = stuntgres.Restore(canceled, target, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	check(c, "SELECT z::TEXT FROM tz_witness WHERE id=2", []string{"TEXT"}, []any{"2004-07-01 16:00:00+00"})
}
