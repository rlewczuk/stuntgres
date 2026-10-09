package reference_test

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

var timezoneControlRows = [][]any{
	{int64(1), "2004-10-19 08:23:54+00", "05:34:17-05"},
	{int64(2), nil, nil},
	{int64(3), "infinity", "24:00:00+02"},
	{int64(4), "-infinity", "00:00:00-05"},
	{int64(5), "0001-01-01 00:00:00+00 BC", "12:00:00+00"},
	{int64(6), "2004-07-01 16:00:00+00", "12:00:00-05"},
}

const timezoneControlQuery = "SELECT id,z::TEXT AS z,q FROM tz_witness ORDER BY id"

func timezoneControlSeed(t *testing.T) *referencePair {
	t.Helper()
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		backend.db.SetMaxOpenConns(1)
		backend.db.SetMaxIdleConns(1)
	}
	p.step(t, referenceExec("SET TIME ZONE 'America/New_York'", 0))
	p.step(t, referenceExec("CREATE TABLE tz_witness(id INTEGER,z TIMESTAMPTZ(3) DEFAULT TIMESTAMPTZ '2004-07-01 12:00',q TIME(3) WITH TIME ZONE DEFAULT TIMETZ '12:00-05')", 0))
	p.step(t, referenceExec("CREATE TABLE tz_empty(v TIMESTAMPTZ)", 0))
	p.step(t, referenceExec("INSERT INTO tz_witness VALUES(1,'2004-10-19 10:23:54+02','05:34:17-05'),(2,NULL,NULL),(3,'infinity','24:00+02'),(4,'-infinity','00:00-05'),(5,'0001-01-01 00:00+00 BC','12:00+00')", 5))
	p.step(t, referenceExec("INSERT INTO tz_witness(id) VALUES(6)", 1))
	return p
}

func TestReferenceTimezoneControlsRoundTripCapture(t *testing.T) {
	p := timezoneControlSeed(t)
	conn, err := p.memory.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	rows, err := conn.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, p.memoryRecord), "SELECT z::TEXT AS z,q FROM tz_witness WHERE id=1")
	if err != nil {
		conn.Close()
		t.Fatal(err)
	}
	defer rows.Close()
	referenceRunStep(t, p.ctx, conn, p.memoryRecord, referenceExec("SET TIME ZONE 'UTC'", 0))
	referenceRunStep(t, p.ctx, conn, p.memoryRecord, referenceExec("SET DateStyle='German'", 0))
	referenceRunStep(t, p.ctx, timezoneAdmittedRows{referenceExecutor: conn, rows: rows}, p.memoryRecord, referenceSelect("SELECT z::TEXT AS z,q FROM tz_witness WHERE id=1", []string{"z", "q"}, []string{"TEXT", "TIMETZ"}, [][]any{{"2004-10-19 04:23:54-04", "05:34:17-05"}}))
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	p.step(t, referenceExec("SET TIME ZONE 'UTC'", 0))
	p.step(t, referenceExec("SET DateStyle='German'", 0))
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"TIMESTAMP(3) WITH TIME ZONE", "TIME(3) WITH TIME ZONE", "tz_empty", "2004-10-19 08:23:54+00", "05:34:17-05", "infinity", "BC", "NULL"} {
		if !strings.Contains(dump.String(), needle) {
			t.Fatal("missing dump witness", needle, dump.String())
		}
	}
	native, target, record := timezoneDestination(t)
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, target, record, s) }
	run(referenceExec("SET TIME ZONE 'Asia/Tokyo'", 0))
	run(referenceExec("SET DateStyle='SQL,DMY'", 0))
	if err := stuntgres.Restore(p.ctx, native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	run(referenceSelect("SHOW TimeZone", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{"Asia/Tokyo"}}))
	run(referenceSelect("SHOW DateStyle", []string{"DateStyle"}, []string{"TEXT"}, [][]any{{"SQL, DMY"}}))
	run(referenceExec("SET TIME ZONE 'UTC'", 0))
	run(referenceExec("SET DateStyle='ISO,MDY'", 0))
	run(referenceSelect(timezoneControlQuery, []string{"id", "z", "q"}, []string{"INT4", "TEXT", "TIMETZ"}, timezoneControlRows))
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, native, &redump); err != nil || redump.String() != dump.String() {
		t.Fatal("redump", err, redump.String(), dump.String())
	}
	// The real PostgreSQL SQL consumer has the same independent formatted rows;
	// its pq raw TIMETZ codec is covered separately, not treated as a native string.
	p.step(t, referenceExec("SET DateStyle='ISO,MDY'", 0))
	timezoneBoth(t, p, referenceSelect("SELECT id,z::TEXT AS z,q::TEXT AS q FROM tz_witness ORDER BY id", []string{"id", "z", "q"}, []string{"INT4", "TEXT", "TEXT"}, timezoneControlRows))
}

func TestReferenceTimezoneControlsRestoreAtomicity(t *testing.T) {
	p := timezoneControlSeed(t)
	p.step(t, referenceExec("SET TIME ZONE 'UTC'", 0))
	p.step(t, referenceExec("SET DateStyle='ISO,MDY'", 0))
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	native, wrapped, record := timezoneDestination(t)
	target, err := wrapped.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if err := stuntgres.Restore(p.ctx, native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	oldRecord := &referenceRecorder{rawBackend: referenceRawNative}
	old, err := target.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, oldRecord), timezoneControlQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	sentinel := referenceSelect(timezoneControlQuery, []string{"id", "z", "q"}, []string{"INT4", "TEXT", "TIMETZ"}, timezoneControlRows)
	unchanged := func() {
		referenceRunStep(t, p.ctx, target, record, sentinel)
		var current bytes.Buffer
		if err := stuntgres.Dump(p.ctx, native, &current); err != nil || current.String() != dump.String() {
			t.Fatal("failed control changed catalog", err, current.String())
		}
	}
	bad := dump.String() + "CREATE TABLE bad(v TIMESTAMPTZ); INSERT INTO bad VALUES('2004-01-01+00'),('nonsense');"
	if err := stuntgres.Restore(p.ctx, native, strings.NewReader(bad)); err == nil {
		t.Fatal("bad restore succeeded")
	}
	unchanged()
	failure := errors.New("read failure")
	if err := stuntgres.Restore(p.ctx, native, timezoneFailingReader{failure}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	unchanged()
	canceled, cancel := context.WithCancel(p.ctx)
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
	if err := stuntgres.Reset(p.ctx, native); err != nil {
		t.Fatal(err)
	}
	referenceRunStep(t, p.ctx, timezoneAdmittedRows{referenceExecutor: target, rows: old}, oldRecord, sentinel)
	if err := stuntgres.Restore(p.ctx, native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	iteration, stop := context.WithCancel(p.ctx)
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
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceSelect("SHOW TimeZone", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{"UTC"}}))
}

func TestReferenceTimezoneNativeDriverOutputEnvironmentCapture(t *testing.T) {
	p := timezoneReferencePair(t)
	p.step(t, referenceExec("UPDATE tz_witness SET z='2004-10-19 08:23:54+00' WHERE id=1", 1))
	conn, err := p.native.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.Raw(func(raw any) error {
		e, q := raw.(driver.ExecerContext), raw.(driver.QueryerContext)
		if _, err := e.ExecContext(p.ctx, "SET TIME ZONE 'America/New_York'", nil); err != nil {
			return err
		}
		rows, err := q.QueryContext(p.ctx, "SELECT z FROM tz_witness", nil)
		if err != nil {
			return err
		}
		defer rows.Close()
		if _, err := e.ExecContext(p.ctx, "SET TIME ZONE 'UTC'", nil); err != nil {
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
		fresh, err := q.QueryContext(p.ctx, "SELECT z FROM tz_witness", nil)
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
