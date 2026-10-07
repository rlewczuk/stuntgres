package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"stuntgres"
	"testing"
	"time"
)

func TestTimezoneControlsRoundTripCaptureAndCancellation(t *testing.T) {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	db := openLocal(t, d, t.Name())
	dest := openLocal(t, d, t.Name()+"/target")
	c, target := timezoneConn(t, db), timezoneConn(t, dest)
	timezoneExec(t, c, "SET TIME ZONE 'America/New_York'", 0)
	timezoneExec(t, c, "CREATE TABLE tz_witness(id INTEGER,z TIMESTAMPTZ(3) DEFAULT TIMESTAMPTZ '2004-07-01 12:00',q TIME(3) WITH TIME ZONE DEFAULT TIMETZ '12:00-05')", 0)
	timezoneExec(t, c, "CREATE TABLE tz_empty(v TIMESTAMPTZ)", 0)
	timezoneExec(t, c, "INSERT INTO tz_witness VALUES(1,'2004-10-19 10:23:54+02','05:34:17-05'),(2,NULL,NULL),(3,'infinity','24:00+02'),(4,'-infinity','00:00-05'),(5,'0001-01-01 00:00+00 BC','12:00+00')", 5)
	timezoneExec(t, c, "INSERT INTO tz_witness(id) VALUES(6)", 1)
	held, err := c.QueryContext(ctx, "SELECT z::TEXT,q FROM tz_witness WHERE id=1")
	if err != nil {
		t.Fatal(err)
	}
	timezoneExec(t, c, "SET TIME ZONE 'UTC'", 0)
	timezoneExec(t, c, "SET DateStyle='German'", 0)
	timeCheckRows(t, held, []string{"z", "q"}, []string{"TEXT", "TIMETZ"}, [][]any{{"2004-10-19 04:23:54-04", "05:34:17-05"}})
	var dump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"TIMESTAMP(3) WITH TIME ZONE", "TIME(3) WITH TIME ZONE", "tz_empty", "2004-10-19 08:23:54+00", "05:34:17-05", "infinity", "BC", "NULL"} {
		if !strings.Contains(dump.String(), needle) {
			t.Fatalf("missing %s in %s", needle, dump.String())
		}
	}
	timezoneExec(t, target, "SET TIME ZONE 'Asia/Tokyo'", 0)
	timezoneExec(t, target, "SET DateStyle='SQL,DMY'", 0)
	if err = stuntgres.Restore(ctx, dest, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	timezoneRows(t, target, "SHOW TimeZone", []string{"TEXT"}, [][]any{{"Asia/Tokyo"}})
	timezoneRows(t, target, "SHOW DateStyle", []string{"TEXT"}, [][]any{{"SQL, DMY"}})
	timezoneExec(t, target, "SET TIME ZONE 'UTC'", 0)
	timezoneExec(t, target, "SET DateStyle='ISO,MDY'", 0)
	want := [][]any{{int64(1), "2004-10-19 08:23:54+00", "05:34:17-05"}, {int64(2), nil, nil}, {int64(3), "infinity", "24:00:00+02"}, {int64(4), "-infinity", "00:00:00-05"}, {int64(5), "0001-01-01 00:00:00+00 BC", "12:00:00+00"}, {int64(6), "2004-07-01 16:00:00+00", "12:00:00-05"}}
	query := "SELECT id,z::TEXT,q FROM tz_witness ORDER BY id"
	timezoneRows(t, target, query, []string{"INT4", "TEXT", "TIMETZ"}, want)
	var again bytes.Buffer
	if err = stuntgres.Dump(ctx, dest, &again); err != nil || again.String() != dump.String() {
		t.Fatal("redump mismatch", err, again.String(), dump.String())
	}
	old, err := target.QueryContext(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	bad := dump.String() + "CREATE TABLE bad(v TIMESTAMPTZ); INSERT INTO bad VALUES('2004-01-01+00'),('nonsense');"
	if err = stuntgres.Restore(ctx, dest, strings.NewReader(bad)); err == nil {
		t.Fatal("bad restore succeeded")
	}
	sentinel := errors.New("read failure")
	if err = stuntgres.Restore(ctx, dest, failingReader{sentinel}); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err = stuntgres.Restore(canceled, dest, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err = stuntgres.Dump(canceled, dest, &bytes.Buffer{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err = target.ExecContext(canceled, "INSERT INTO tz_witness(id) VALUES(7)"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	timezoneRows(t, target, query, []string{"INT4", "TEXT", "TIMETZ"}, want)
	if err = stuntgres.Reset(ctx, dest); err != nil {
		t.Fatal(err)
	}
	timeCheckRows(t, old, []string{"id", "z", "q"}, []string{"INT4", "TEXT", "TIMETZ"}, want)
	if err = stuntgres.Restore(ctx, dest, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	iteration, stop := context.WithCancel(ctx)
	rows, err := target.QueryContext(iteration, query)
	if err != nil {
		t.Fatal(err)
	}
	stop()
	for rows.Next() {
	}
	if !errors.Is(rows.Err(), context.Canceled) {
		t.Fatal(rows.Err())
	}
	rows.Close()
	timezoneRows(t, c, "SHOW TimeZone", []string{"TEXT"}, [][]any{{"UTC"}})
}

func TestTimezoneNativeDriverOutputEnvironmentCapture(t *testing.T) {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	conn, err := d.Open(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	e := conn.(driver.ExecerContext)
	q := conn.(driver.QueryerContext)
	for _, sql := range []string{"CREATE TABLE tz_witness(z TIMESTAMPTZ)", "INSERT INTO tz_witness VALUES('2004-10-19 08:23:54+00')", "SET TIME ZONE 'America/New_York'"} {
		if _, err = e.ExecContext(ctx, sql, nil); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := q.QueryContext(ctx, "SELECT z FROM tz_witness", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if _, err = e.ExecContext(ctx, "SET TIME ZONE 'UTC'", nil); err != nil {
		t.Fatal(err)
	}
	values := make([]driver.Value, 1)
	if err = rows.Next(values); err != nil {
		t.Fatal(err)
	}
	stamp, ok := values[0].(time.Time)
	if !ok {
		t.Fatal(values)
	}
	_, offset := stamp.Zone()
	if offset != -4*3600 || stamp.Hour() != 4 || !stamp.Equal(time.Date(2004, 10, 19, 8, 23, 54, 0, time.UTC)) {
		t.Fatal(stamp, offset)
	}
	if err = rows.Next(values); err != io.EOF {
		t.Fatal(err)
	}
	fresh, err := q.QueryContext(ctx, "SELECT z FROM tz_witness", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if err = fresh.Next(values); err != nil {
		t.Fatal(err)
	}
	stamp = values[0].(time.Time)
	_, offset = stamp.Zone()
	if offset != 0 || stamp.Hour() != 8 {
		t.Fatal(stamp)
	}
}
