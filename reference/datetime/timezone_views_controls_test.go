package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTimezoneViewsControls(t *testing.T) {
	var symbolicDump string
	timezoneViewsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"CREATE VIEW timezone_control_names AS SELECT * FROM pg_catalog.pg_timezone_names WHERE name='UTC'",
			"CREATE VIEW timezone_control_abbrevs AS SELECT * FROM pg_catalog.pg_timezone_abbrevs WHERE abbrev='IST'",
			"CREATE TABLE timezone_control_sentinel(id int)",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO timezone_control_sentinel VALUES(7)", 1))
		nameQuery := "SELECT * FROM timezone_control_names"
		abbrevQuery := "SELECT * FROM timezone_control_abbrevs"
		run(timezoneViewsNames(nameQuery, [][]any{{"UTC", "UTC", "00:00:00", false}}))
		run(fixture.Prepared(timezoneViewsAbbrevs(abbrevQuery, [][]any{{"IST", "02:00:00", false}})))
		if b.PG {
			// Reset/Dump/Restore are native APIs. Replay their actual symbolic SQL
			// on PostgreSQL instead of inventing a PostgreSQL control equivalent.
			if symbolicDump == "" {
				t.Fatal("native symbolic dump missing")
			}
			for _, q := range []string{"DROP VIEW timezone_control_names", "DROP VIEW timezone_control_abbrevs", "DROP TABLE timezone_control_sentinel"} {
				run(fixture.Exec(q, 0))
			}
			if _, err := c.ExecContext(p.Ctx, symbolicDump); err != nil {
				t.Fatal("PostgreSQL dump replay", err, symbolicDump)
			}
			run(timezoneViewsNames(nameQuery, [][]any{{"UTC", "UTC", "00:00:00", false}}))
			run(fixture.Exec("SET timezone_abbreviations='India'", 0))
			run(timezoneViewsAbbrevs(abbrevQuery, [][]any{{"IST", "05:30:00", false}}))
			return
		}
		oldRecord := &fixture.Recorder{RawBackend: fixture.RawNative}
		rows, err := c.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, oldRecord), abbrevQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		symbolicDump = dump.String()
		if !strings.Contains(symbolicDump, "pg_timezone_names") || !strings.Contains(symbolicDump, "pg_timezone_abbrevs") || strings.Contains(symbolicDump, "CREATE TABLE pg_catalog.pg_timezone") || strings.Contains(symbolicDump, "INSERT INTO pg_catalog.pg_timezone") {
			t.Fatal("not a symbolic user-view dump", symbolicDump)
		}
		run(fixture.Exec("SET timezone_abbreviations='India'", 0))
		run(fixture.Exec("SET TIME ZONE 'Asia/Kolkata'", 0))
		run(fixture.Exec("SET IntervalStyle='iso_8601'", 0))
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		run(timezoneViewsNames("SELECT * FROM pg_timezone_names WHERE name='UTC'", [][]any{{"UTC", "UTC", "PT0S", false}}))
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(symbolicDump)); err != nil {
			t.Fatal(err)
		}
		run(timezoneAbbreviationsShow("India"))
		run(fixture.Select("SHOW TimeZone", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{"Asia/Kolkata"}}))
		run(timezoneViewsAbbrevs(abbrevQuery, [][]any{{"IST", "PT5H30M", false}}))
		fixture.RunStep(t, p.Ctx, timezoneAdmittedRows{Executor: c, rows: rows}, oldRecord, timezoneViewsAbbrevs(abbrevQuery, [][]any{{"IST", "02:00:00", false}}))
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE broken(id int); INSERT INTO missing VALUES(1);")); err == nil {
			t.Fatal("invalid Restore succeeded")
		}
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(canceled, p.Native, strings.NewReader(symbolicDump)); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		run(fixture.Select("SELECT id FROM timezone_control_sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(timezoneViewsAbbrevs(abbrevQuery, [][]any{{"IST", "PT5H30M", false}}))
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || redump.String() != symbolicDump {
			t.Fatal("symbolic redump changed", err, redump.String(), symbolicDump)
		}
	})
}
