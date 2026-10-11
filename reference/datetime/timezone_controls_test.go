package reference_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

var timezoneControlRows = [][]any{
	{int64(1), "2004-10-19 08:23:54+00", "05:34:17-05"},
	{int64(2), nil, nil},
	{int64(3), "infinity", "24:00:00+02"},
	{int64(4), "-infinity", "00:00:00-05"},
	{int64(5), "0001-01-01 00:00:00+00 BC", "12:00:00+00"},
	{int64(6), "2004-07-01 16:00:00+00", "12:00:00-05"},
}

const timezoneControlQuery = "SELECT id,z::TEXT AS z,q FROM tz_witness ORDER BY id"

func timezoneControlSeed(t *testing.T) *fixture.Pair {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		backend.DB.SetMaxOpenConns(1)
		backend.DB.SetMaxIdleConns(1)
	}
	p.Step(t, fixture.Exec("SET TIME ZONE 'America/New_York'", 0))
	p.Step(t, fixture.Exec("CREATE TABLE tz_witness(id INTEGER,z TIMESTAMPTZ(3) DEFAULT TIMESTAMPTZ '2004-07-01 12:00',q TIME(3) WITH TIME ZONE DEFAULT TIMETZ '12:00-05')", 0))
	p.Step(t, fixture.Exec("CREATE TABLE tz_empty(v TIMESTAMPTZ)", 0))
	p.Step(t, fixture.Exec("INSERT INTO tz_witness VALUES(1,'2004-10-19 10:23:54+02','05:34:17-05'),(2,NULL,NULL),(3,'infinity','24:00+02'),(4,'-infinity','00:00-05'),(5,'0001-01-01 00:00+00 BC','12:00+00')", 5))
	p.Step(t, fixture.Exec("INSERT INTO tz_witness(id) VALUES(6)", 1))
	return p
}

func TestReferenceTimezoneControlsRoundTripCapture(t *testing.T) {
	p := timezoneControlSeed(t)
	conn, err := p.Memory.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	rows, err := conn.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, p.MemoryRecord), "SELECT z::TEXT AS z,q FROM tz_witness WHERE id=1")
	if err != nil {
		conn.Close()
		t.Fatal(err)
	}
	defer rows.Close()
	fixture.RunStep(t, p.Ctx, conn, p.MemoryRecord, fixture.Exec("SET TIME ZONE 'UTC'", 0))
	fixture.RunStep(t, p.Ctx, conn, p.MemoryRecord, fixture.Exec("SET DateStyle='German'", 0))
	fixture.RunStep(t, p.Ctx, timezoneAdmittedRows{Executor: conn, rows: rows}, p.MemoryRecord, fixture.Select("SELECT z::TEXT AS z,q FROM tz_witness WHERE id=1", []string{"z", "q"}, []string{"TEXT", "TIMETZ"}, [][]any{{"2004-10-19 04:23:54-04", "05:34:17-05"}}))
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	p.Step(t, fixture.Exec("SET TIME ZONE 'UTC'", 0))
	p.Step(t, fixture.Exec("SET DateStyle='German'", 0))
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"TIMESTAMP(3) WITH TIME ZONE", "TIME(3) WITH TIME ZONE", "tz_empty", "2004-10-19 08:23:54+00", "05:34:17-05", "infinity", "BC", "NULL"} {
		if !strings.Contains(dump.String(), needle) {
			t.Fatal("missing dump witness", needle, dump.String())
		}
	}
	native, target, record := timezoneDestination(t)
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, target, record, s) }
	run(fixture.Exec("SET TIME ZONE 'Asia/Tokyo'", 0))
	run(fixture.Exec("SET DateStyle='SQL,DMY'", 0))
	if err := stuntgres.Restore(p.Ctx, native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	run(fixture.Select("SHOW TimeZone", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{"Asia/Tokyo"}}))
	run(fixture.Select("SHOW DateStyle", []string{"DateStyle"}, []string{"TEXT"}, [][]any{{"SQL, DMY"}}))
	run(fixture.Exec("SET TIME ZONE 'UTC'", 0))
	run(fixture.Exec("SET DateStyle='ISO,MDY'", 0))
	run(fixture.Select(timezoneControlQuery, []string{"id", "z", "q"}, []string{"INT4", "TEXT", "TIMETZ"}, timezoneControlRows))
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, native, &redump); err != nil || redump.String() != dump.String() {
		t.Fatal("redump", err, redump.String(), dump.String())
	}
	// The real PostgreSQL SQL consumer has the same independent formatted rows;
	// its pq raw TIMETZ codec is covered separately, not treated as a native string.
	p.Step(t, fixture.Exec("SET DateStyle='ISO,MDY'", 0))
	timezoneBoth(t, p, fixture.Select("SELECT id,z::TEXT AS z,q::TEXT AS q FROM tz_witness ORDER BY id", []string{"id", "z", "q"}, []string{"INT4", "TEXT", "TEXT"}, timezoneControlRows))
}

func TestReferenceTimezoneControlsRestoreAtomicity(t *testing.T) {
	p := timezoneControlSeed(t)
	p.Step(t, fixture.Exec("SET TIME ZONE 'UTC'", 0))
	p.Step(t, fixture.Exec("SET DateStyle='ISO,MDY'", 0))
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	native, wrapped, record := timezoneDestination(t)
	target, err := wrapped.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if err := stuntgres.Restore(p.Ctx, native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	oldRecord := &fixture.Recorder{RawBackend: fixture.RawNative}
	old, err := target.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, oldRecord), timezoneControlQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	sentinel := fixture.Select(timezoneControlQuery, []string{"id", "z", "q"}, []string{"INT4", "TEXT", "TIMETZ"}, timezoneControlRows)
	unchanged := func() {
		fixture.RunStep(t, p.Ctx, target, record, sentinel)
		var current bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, native, &current); err != nil || current.String() != dump.String() {
			t.Fatal("failed control changed catalog", err, current.String())
		}
	}
	bad := dump.String() + "CREATE TABLE bad(v TIMESTAMPTZ); INSERT INTO bad VALUES('2004-01-01+00'),('nonsense');"
	if err := stuntgres.Restore(p.Ctx, native, strings.NewReader(bad)); err == nil {
		t.Fatal("bad restore succeeded")
	}
	unchanged()
	failure := errors.New("read failure")
	if err := stuntgres.Restore(p.Ctx, native, timezoneFailingReader{failure}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	unchanged()
	canceled, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err := stuntgres.Restore(canceled, native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	unchanged()
	if err := stuntgres.Dump(canceled, native, &bytes.Buffer{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	unchanged()
	if _, err := target.ExecContext(canceled, "INSERT INTO tz_witness(id) VALUES(7)"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	unchanged()
	if err := stuntgres.Reset(p.Ctx, native); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, timezoneAdmittedRows{Executor: target, rows: old}, oldRecord, sentinel)
	if err := stuntgres.Restore(p.Ctx, native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	iteration, stop := context.WithCancel(p.Ctx)
	rows, err := target.QueryContext(iteration, timezoneControlQuery)
	if err != nil {
		stop()
		t.Fatal(err)
	}
	defer rows.Close()
	stop()
	if rows.Next() {
		t.Fatal("canceled cursor exported a row")
	}
	if !errors.Is(rows.Err(), context.Canceled) {
		t.Fatal(rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	unchanged()
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Select("SHOW TimeZone", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{"UTC"}}))
}

func TestReferenceTimezoneNativeDriverOutputEnvironmentCapture(t *testing.T) {
	p := fixture.TimezoneReferencePair(t)
	p.Step(t, fixture.Exec("UPDATE tz_witness SET z='2004-10-19 08:23:54+00' WHERE id=1", 1))
	conn, err := p.Native.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.Raw(func(raw any) error {
		e, q := raw.(driver.ExecerContext), raw.(driver.QueryerContext)
		if _, err := e.ExecContext(p.Ctx, "SET TIME ZONE 'America/New_York'", nil); err != nil {
			return err
		}
		rows, err := q.QueryContext(p.Ctx, "SELECT z FROM tz_witness", nil)
		if err != nil {
			return err
		}
		defer rows.Close()
		if _, err := e.ExecContext(p.Ctx, "SET TIME ZONE 'UTC'", nil); err != nil {
			return err
		}
		values := make([]driver.Value, 1)
		if err := rows.Next(values); err != nil {
			return err
		}
		stamp, ok := values[0].(time.Time)
		if !ok {
			t.Fatal(values)
		}
		_, offset := stamp.Zone()
		if offset != -4*3600 || stamp.Hour() != 4 || !stamp.Equal(time.Date(2004, 10, 19, 8, 23, 54, 0, time.UTC)) {
			t.Fatal(stamp, offset)
		}
		if err := rows.Next(values); err != io.EOF {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		fresh, err := q.QueryContext(p.Ctx, "SELECT z FROM tz_witness", nil)
		if err != nil {
			return err
		}
		defer fresh.Close()
		if err := fresh.Next(values); err != nil {
			return err
		}
		stamp, ok = values[0].(time.Time)
		if !ok {
			t.Fatal(values)
		}
		_, offset = stamp.Zone()
		if offset != 0 || stamp.Hour() != 8 {
			t.Fatal(stamp)
		}
		if err := fresh.Next(values); err != io.EOF {
			t.Fatal(err)
		}
		return fresh.Close()
	}); err != nil {
		t.Fatal(err)
	}
}
