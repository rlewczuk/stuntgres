package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceDatetimeZoneFrozenLeavesAndRetainedOutput(t *testing.T) {
	datetimeZoneScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		literalSQL := "SELECT date_add(TIMESTAMPTZ '2004-07-01 12:00',INTERVAL '0','UTC')::TEXT AS v FROM tz_witness WHERE id=1"
		parameterSQL := "SELECT date_add($1::TIMESTAMPTZ,INTERVAL '0','UTC')::TEXT AS v FROM tz_witness WHERE id=1"
		literal := timezoneHeld(t, p, c, literalSQL)
		parameter := timezoneHeld(t, p, c, parameterSQL)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET TIME ZONE 'America/New_York'", 0))
		fixture.RunStep(t, p.Ctx, literal, b.Recorder, fixture.Select(literalSQL, []string{"v"}, []string{"TEXT"}, [][]any{{"2004-07-01 08:00:00-04"}}))
		fixture.RunStep(t, p.Ctx, parameter, b.Recorder, fixture.Select(parameterSQL, []string{"v"}, []string{"TEXT"}, [][]any{{"2004-07-01 12:00:00-04"}}, "2004-07-01 12:00"))
		if b.PG {
			return
		} // Native eager cursor ownership, not pq session races.
		query := "SELECT timezone(TIMESTAMP '2004-07-01 12:00') AS result FROM tz_witness WHERE id=1"
		record := &fixture.Recorder{RawBackend: fixture.RawNative}
		old, err := c.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, record), query)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		// The existing native control authority permits replacement while an eager
		// immutable result is retained; no live per-row session reads are allowed.
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		// Consume through the existing common recorder rather than a private row path.
		want := fixture.Select(query, []string{"result"}, []string{"TIMESTAMPTZ"}, [][]any{{time.Date(2004, 7, 1, 16, 0, 0, 0, time.UTC)}})
		want.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
			var result sql.NullTime
			if err := rows.Scan(&result); err != nil {
				t.Fatal(err)
			}
			_, east := result.Time.Zone()
			if !result.Valid || east != -4*3600 || result.Time.Hour() != 12 {
				t.Fatal("captured native output", result, east)
			}
		}
		fixture.RunStep(t, p.Ctx, timezoneAdmittedRows{Executor: c, rows: old}, record, want)
	})
}

func TestReferenceDatetimeZonePrecancellation(t *testing.T) {
	datetimeZoneScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		if b.PG {
			return
		} // Ordinary native API cancellation, not lib/pq timing parity.
		readSQL := "SELECT timezone('UTC',TIMESTAMP '2004-01-01') AS v FROM tz_witness"
		writeSQL := "INSERT INTO tz_witness(id,z) VALUES(2,date_add(TIMESTAMPTZ '2004-01-01+00',INTERVAL '1 day','UTC'))"
		read := timezoneHeld(t, p, c, readSQL)
		write := timezoneHeld(t, p, c, writeSQL)
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if stmt, err := c.PrepareContext(ctx, readSQL); stmt != nil || !errors.Is(err, context.Canceled) {
			if stmt != nil {
				stmt.Close()
			}
			t.Fatal(stmt, err)
		}
		for _, x := range []fixture.Executor{c, read} {
			rows, err := x.QueryContext(ctx, readSQL)
			if rows != nil || !errors.Is(err, context.Canceled) {
				if rows != nil {
					rows.Close()
				}
				t.Fatal(rows, err)
			}
		}
		for _, x := range []fixture.Executor{c, write} {
			result, err := x.ExecContext(ctx, writeSQL)
			if result != nil || !errors.Is(err, context.Canceled) {
				t.Fatal(result, err)
			}
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
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id FROM tz_witness ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		fixture.RunStep(t, p.Ctx, write, b.Recorder, fixture.Exec(writeSQL, 1))
	})
}
