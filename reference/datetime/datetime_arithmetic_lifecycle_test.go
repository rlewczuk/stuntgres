package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

const datetimeArithmeticStoredDDL = "CREATE TABLE dt_stored(id INTEGER,d DATE DEFAULT DATE '2024-02-28'+1,s TIMESTAMP DEFAULT TIMESTAMP '2024-01-31 12:00'+INTERVAL '1 month',z TIMESTAMPTZ DEFAULT TIMESTAMPTZ '2005-04-02 12:00-07'+INTERVAL '1 day',v INTERVAL DEFAULT INTERVAL '1 month'+INTERVAL '30 days',q TIMETZ DEFAULT TIMETZ '23:00+02'+INTERVAL '3 hours',CHECK(d+1>d))"
const datetimeArithmeticStoredQuery = "SELECT id,d::TEXT AS d,s::TEXT AS s,(z AT TIME ZONE 'UTC')::TEXT AS z,v,q::TEXT AS q FROM dt_stored ORDER BY id"

var datetimeArithmeticStoredRows = [][]any{
	{int64(1), "2024-02-29", "2024-02-29 12:00:00", "2005-04-03 18:00:00", "1 mon 30 days", "02:00:00+02"},
	{int64(2), "2024-02-29", "2024-02-29 12:00:00", "2005-04-03 19:00:00", "1 mon 30 days", "02:00:00+02"},
	{int64(3), nil, nil, nil, nil, nil},
	{int64(4), nil, "infinity", "-infinity", "infinity", nil},
}

func datetimeArithmeticStoredSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("SET TIME ZONE 'America/Denver'", 0))
	run(fixture.Exec(datetimeArithmeticStoredDDL, 0))
	run(fixture.Exec("INSERT INTO dt_stored(id) VALUES(1)", 1))
	run(fixture.Exec("SET TIME ZONE 'UTC'", 0))
	run(fixture.Prepared(fixture.Exec("INSERT INTO dt_stored(id) VALUES($1)", 1, 2)))
	run(fixture.Prepared(fixture.Exec("INSERT INTO dt_stored VALUES($1,$2,$3,$4,$5,$6)", 1, 3, nil, nil, nil, nil, nil)))
	run(fixture.Exec("INSERT INTO dt_stored VALUES(4,NULL,'infinity','-infinity','infinity',NULL)", 1))
	run(fixture.Exec("CREATE VIEW dt_stored_view AS SELECT id,d+1 AS next_day,s+INTERVAL '1 day' AS next_stamp,z+INTERVAL '0' AS instant,v+INTERVAL '1 hour' AS amount,q+INTERVAL '1 hour' AS clock FROM dt_stored", 0))
	datetimeArithmeticStoredCheck(t, p, b, c)
}

func datetimeArithmeticStoredCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor) {
	t.Helper()
	datetimeArithmeticBoth(t, p, b, x, fixture.Select(datetimeArithmeticStoredQuery, []string{"id", "d", "s", "z", "v", "q"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "INTERVAL", "TEXT"}, datetimeArithmeticStoredRows))
	datetimeArithmeticBoth(t, p, b, x, fixture.Select("SELECT id,next_day::TEXT AS d,next_stamp::TEXT AS s,(instant AT TIME ZONE 'UTC')::TEXT AS z,amount,clock::TEXT AS q FROM dt_stored_view ORDER BY id", []string{"id", "d", "s", "z", "amount", "q"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "INTERVAL", "TEXT"}, [][]any{
		{int64(1), "2024-03-01", "2024-03-01 12:00:00", "2005-04-03 18:00:00", "1 mon 30 days 01:00:00", "03:00:00+02"},
		{int64(2), "2024-03-01", "2024-03-01 12:00:00", "2005-04-03 19:00:00", "1 mon 30 days 01:00:00", "03:00:00+02"},
		{int64(3), nil, nil, nil, nil, nil}, {int64(4), nil, "infinity", "-infinity", "infinity", nil},
	}))
}

func datetimeArithmeticStoredControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	if b.PG {
		return
	} // Native controls have no PostgreSQL API analogue.
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"DEFAULT", "INTERVAL", "dt_stored_view", "CHECK", "infinity", "NULL", "30 days"} {
		if !strings.Contains(dump.String(), needle) {
			t.Fatal("lost symbolic/value witness", needle, dump.String())
		}
	}
	native, wrapped, record := timezoneDestination(t)
	target, err := wrapped.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	tb := fixture.Backend{Name: "destination", DB: wrapped, Recorder: record}
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, target, record, s) }
	for _, q := range []string{"SET TIME ZONE 'America/New_York'", "SET DateStyle='SQL,DMY'", "SET IntervalStyle='iso_8601'"} {
		run(fixture.Exec(q, 0))
	}
	if err := stuntgres.Restore(p.Ctx, native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	run(fixture.Select("SHOW TimeZone", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{"America/New_York"}}))
	run(fixture.Select("SHOW DateStyle", []string{"DateStyle"}, []string{"TEXT"}, [][]any{{"SQL, DMY"}}))
	run(fixture.Select("SHOW IntervalStyle", []string{"IntervalStyle"}, []string{"TEXT"}, [][]any{{"iso_8601"}}))
	run(fixture.Exec("SET DateStyle='ISO,MDY'", 0))
	run(fixture.Exec("SET IntervalStyle='postgres'", 0))
	datetimeArithmeticStoredCheck(t, p, tb, target)
	fixture.IntervalDumpMatches(t, p.Ctx, native, dump.Bytes())
	oldRecord := &fixture.Recorder{RawBackend: fixture.RawNative}
	old, err := target.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, oldRecord), datetimeArithmeticStoredQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	bad := dump.String() + "CREATE TABLE dt_restore_bad(v INTERVAL); INSERT INTO dt_restore_bad VALUES(INTERVAL '1 hour'/0);"
	if err := stuntgres.Restore(p.Ctx, native, strings.NewReader(bad)); err == nil {
		t.Fatal("failed arithmetic restore succeeded")
	} else {
		fixture.AssertDiagnostic(t, err, diagnosticDatetimePointer("22012", "division by zero"))
	}
	fixture.IntervalDumpMatches(t, p.Ctx, native, dump.Bytes())
	failure := errors.New("datetime restore reader failure")
	if err := stuntgres.Restore(p.Ctx, native, timezoneFailingReader{failure}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	fixture.IntervalDumpMatches(t, p.Ctx, native, dump.Bytes())
	if err := stuntgres.Reset(p.Ctx, native); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, timezoneAdmittedRows{Executor: target, rows: old}, oldRecord, fixture.Select(datetimeArithmeticStoredQuery, []string{"id", "d", "s", "z", "v", "q"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "INTERVAL", "TEXT"}, datetimeArithmeticStoredRows))
	if err := stuntgres.Restore(p.Ctx, native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	fixture.IntervalDumpMatches(t, p.Ctx, native, dump.Bytes())
	run(fixture.Exec("INSERT INTO dt_stored(id) VALUES(5)", 1))
	run(fixture.Select("SELECT (z AT TIME ZONE 'UTC')::TEXT AS z FROM dt_stored WHERE id=5", []string{"z"}, []string{"TEXT"}, [][]any{{"2005-04-03 18:00:00"}}))
	// Replay the ACTUAL emitted native SQL, never a hand-maintained equivalent.
	if p.Postgres != nil {
		pg, err := p.Postgres.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer pg.Close()
		if _, err := pg.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("emitted SQL replay", err, dump.String())
		}
		pb := fixture.Backend{Name: "replay", DB: p.Postgres, Recorder: p.PostgresRecord, PG: true}
		for _, q := range []string{"SET TIME ZONE 'UTC'", "SET DateStyle='ISO,MDY'", "SET IntervalStyle='postgres'"} {
			fixture.RunStep(t, p.Ctx, pg, pb.Recorder, fixture.Exec(q, 0))
		}
		datetimeArithmeticStoredCheck(t, p, pb, pg)
		for _, q := range []string{"DROP VIEW dt_stored_view", "DROP TABLE dt_stored"} {
			fixture.RunStep(t, p.Ctx, pg, pb.Recorder, fixture.Exec(q, 0))
		}
	}
}

func diagnosticDatetimePointer(code, message string) *fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic(code, message)
	return &d
}

func TestReferenceDatetimeArithmeticStoredExpressionsAndControls(t *testing.T) {
	datetimeArithmeticScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		datetimeArithmeticStoredSeed(t, p, b, c)
		datetimeArithmeticStoredControls(t, p, b, c)
	})
}

func TestReferenceDatetimeArithmeticVolatilityConsumers(t *testing.T) {
	datetimeArithmeticScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE dt_stable(id INTEGER,flag BOOLEAN,z TIMESTAMPTZ DEFAULT TIMESTAMPTZ '2005-04-02 12:00-07'+INTERVAL '1 day')", 0))
		run(fixture.Exec("INSERT INTO dt_stable(id,flag) VALUES(1,TRUE)", 1))
		for _, expr := range []string{
			"CASE WHEN flag THEN TIMESTAMPTZ '2005-04-02 12:00-07'+INTERVAL '1 day' ELSE NULL::TIMESTAMPTZ END",
			"COALESCE(NULL::TIMESTAMPTZ,TIMESTAMPTZ '2005-04-02 12:00-07'+INTERVAL '1 day')",
			"CASE WHEN flag THEN INTERVAL '1 day'+TIMESTAMPTZ '2005-04-02 12:00-07' ELSE NULL::TIMESTAMPTZ END",
		} {
			q := "SELECT (" + expr + ")::TEXT AS result FROM dt_stable WHERE id=1"
			h := timezoneHeld(t, p, c, q)
			for _, tc := range []struct{ zone, want string }{{"America/Denver", "2005-04-03 12:00:00-06"}, {"UTC", "2005-04-03 19:00:00+00"}} {
				run(fixture.Exec("SET TIME ZONE '"+tc.zone+"'", 0))
				fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.Select(q, []string{"result"}, []string{"TEXT"}, [][]any{{tc.want}}))
			}
		}
		// IntrinsicStableDDL asserts the complete direct/prepared refusal records.
		run(fixture.Exec("CREATE TABLE dt_immutable(s TIMESTAMP,z TIMESTAMPTZ,g TIMESTAMP GENERATED ALWAYS AS (s+INTERVAL '1 day') STORED,h INTERVAL GENERATED ALWAYS AS (z-TIMESTAMPTZ '2000-01-01+00') STORED)", 0))
		run(fixture.Exec("CREATE INDEX dt_immutable_s ON dt_immutable ((s+INTERVAL '1 day'))", 0))
		run(fixture.Exec("CREATE INDEX dt_immutable_z ON dt_immutable ((z-TIMESTAMPTZ '2000-01-01+00'))", 0))
		run(fixture.Exec("INSERT INTO dt_immutable(s,z) VALUES('2024-01-01','2000-01-02+00')", 1))
		datetimeArithmeticBoth(t, p, b, c, fixture.Select("SELECT g::TEXT AS g,h FROM dt_immutable", []string{"g", "h"}, []string{"TEXT", "INTERVAL"}, [][]any{{"2024-01-02 00:00:00", "1 day"}}))
	})
}

func TestReferenceDatetimeArithmeticCancellation(t *testing.T) {
	datetimeArithmeticScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE dt_cancel(v INTERVAL)", 0))
		run(fixture.Exec("INSERT INTO dt_cancel VALUES('1 hour')", 1))
		if b.PG {
			return
		} // Precancellation is a native adapter contract, not a pq race.
		q := "SELECT v/0 AS result FROM dt_cancel"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		for _, x := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}} {
			rows, err := x.QueryContext(ctx, q)
			if rows != nil || !errors.Is(err, context.Canceled) {
				if rows != nil {
					rows.Close()
				}
				t.Fatal(rows, err)
			}
		}
		if result, err := c.ExecContext(ctx, "INSERT INTO dt_cancel VALUES(INTERVAL '1 hour'/0)"); result != nil || !errors.Is(err, context.Canceled) {
			t.Fatal(result, err)
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Dump(ctx, p.Native, &bytes.Buffer{}); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(ctx, p.Native); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		fixture.IntervalDumpMatches(t, p.Ctx, p.Native, dump.Bytes())
		run(fixture.Select("SELECT v FROM dt_cancel", []string{"v"}, []string{"INTERVAL"}, [][]any{{"01:00:00"}}))
	})
}

func TestReferenceDatetimeArithmeticNativeOutputCapture(t *testing.T) {
	datetimeArithmeticScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		if b.PG {
			return
		}
		raw, err := p.Native.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer raw.Close()
		if err := raw.Raw(func(conn any) error {
			e, q := conn.(driver.ExecerContext), conn.(driver.QueryerContext)
			if _, err := e.ExecContext(p.Ctx, "SET TIME ZONE 'America/Denver'", nil); err != nil {
				return err
			}
			if _, err := e.ExecContext(p.Ctx, "SET IntervalStyle='postgres'", nil); err != nil {
				return err
			}
			rows, err := q.QueryContext(p.Ctx, "SELECT TIMESTAMPTZ '2005-04-02 12:00-07'+INTERVAL '1 day' AS result,INTERVAL '1 month'+INTERVAL '30 days' AS amount,TIMETZ '23:00+02'+INTERVAL '3 hours' AS clock", nil)
			if err != nil {
				return err
			}
			defer rows.Close()
			meta := rows.(driver.RowsColumnTypeDatabaseTypeName)
			for i, typ := range []string{"TIMESTAMPTZ", "INTERVAL", "TIMETZ"} {
				if meta.ColumnTypeDatabaseTypeName(i) != typ {
					t.Fatal("raw result type", i, meta.ColumnTypeDatabaseTypeName(i))
				}
			}
			if _, err := e.ExecContext(p.Ctx, "SET TIME ZONE 'UTC'", nil); err != nil {
				return err
			}
			if _, err := e.ExecContext(p.Ctx, "SET IntervalStyle='iso_8601'", nil); err != nil {
				return err
			}
			values := make([]driver.Value, 3)
			if err := rows.Next(values); err != nil {
				return err
			}
			v, ok := values[0].(time.Time)
			if !ok {
				t.Fatal(values)
			}
			_, offset := v.Zone()
			if !v.Equal(time.Date(2005, 4, 3, 18, 0, 0, 0, time.UTC)) || v.Hour() != 12 || offset != -6*3600 {
				t.Fatal("captured arithmetic cursor", v, offset)
			}
			if values[1] != "1 mon 30 days" || values[2] != "02:00:00+02" {
				t.Fatal("captured interval/clock output", values)
			}
			if err := rows.Next(values); err != io.EOF {
				t.Fatal(err)
			}
			return rows.Close()
		}); err != nil {
			t.Fatal(err)
		}
	})
}
