package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceTimezoneViewsControls(t *testing.T) {
	var symbolicDump string
	timezoneViewsScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c referenceExecutor) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		for _, q := range []string{
			"CREATE VIEW timezone_control_names AS SELECT * FROM pg_catalog.pg_timezone_names WHERE name='UTC'",
			"CREATE VIEW timezone_control_abbrevs AS SELECT * FROM pg_catalog.pg_timezone_abbrevs WHERE abbrev='IST'",
			"CREATE TABLE timezone_control_sentinel(id int)",
		} {
			run(referenceExec(q, 0))
		}
		run(referenceExec("INSERT INTO timezone_control_sentinel VALUES(7)", 1))
		nameQuery := "SELECT * FROM timezone_control_names"
		abbrevQuery := "SELECT * FROM timezone_control_abbrevs"
		run(timezoneViewsNames(nameQuery, [][]any{{"UTC", "UTC", "00:00:00", false}}))
		run(referencePrepared(timezoneViewsAbbrevs(abbrevQuery, [][]any{{"IST", "02:00:00", false}})))
		if b.pg {
			// Reset/Dump/Restore are native APIs. Replay their actual symbolic SQL
			// on PostgreSQL instead of inventing a PostgreSQL control equivalent.
			if symbolicDump == "" {
				t.Fatal("native symbolic dump missing")
			}
			for _, q := range []string{"DROP VIEW timezone_control_names", "DROP VIEW timezone_control_abbrevs", "DROP TABLE timezone_control_sentinel"} {
				run(referenceExec(q, 0))
			}
			if _, err := c.ExecContext(p.ctx, symbolicDump); err != nil {
				t.Fatal("PostgreSQL dump replay", err, symbolicDump)
			}
			run(timezoneViewsNames(nameQuery, [][]any{{"UTC", "UTC", "00:00:00", false}}))
			run(referenceExec("SET timezone_abbreviations='India'", 0))
			run(timezoneViewsAbbrevs(abbrevQuery, [][]any{{"IST", "05:30:00", false}}))
			return
		}
		oldRecord := &referenceRecorder{rawBackend: referenceRawNative}
		rows, err := c.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, oldRecord), abbrevQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
			t.Fatal(err)
		}
		symbolicDump = dump.String()
		if !strings.Contains(symbolicDump, "pg_timezone_names") || !strings.Contains(symbolicDump, "pg_timezone_abbrevs") || strings.Contains(symbolicDump, "CREATE TABLE pg_catalog.pg_timezone") || strings.Contains(symbolicDump, "INSERT INTO pg_catalog.pg_timezone") {
			t.Fatal("not a symbolic user-view dump", symbolicDump)
		}
		run(referenceExec("SET timezone_abbreviations='India'", 0))
		run(referenceExec("SET TIME ZONE 'Asia/Kolkata'", 0))
		run(referenceExec("SET IntervalStyle='iso_8601'", 0))
		if err := stuntgres.Reset(p.ctx, p.native); err != nil {
			t.Fatal(err)
		}
		run(timezoneViewsNames("SELECT * FROM pg_timezone_names WHERE name='UTC'", [][]any{{"UTC", "UTC", "PT0S", false}}))
		if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(symbolicDump)); err != nil {
			t.Fatal(err)
		}
		run(timezoneAbbreviationsShow("India"))
		run(referenceSelect("SHOW TimeZone", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{"Asia/Kolkata"}}))
		run(timezoneViewsAbbrevs(abbrevQuery, [][]any{{"IST", "PT5H30M", false}}))
		referenceRunStep(t, p.ctx, timezoneAdmittedRows{referenceExecutor: c, rows: rows}, oldRecord, timezoneViewsAbbrevs(abbrevQuery, [][]any{{"IST", "02:00:00", false}}))
		if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE broken(id int); INSERT INTO missing VALUES(1);")); err == nil {
			t.Fatal("invalid Restore succeeded")
		}
		canceled, cancel := context.WithCancel(p.ctx)
		cancel()
		if err := stuntgres.Restore(canceled, p.native, strings.NewReader(symbolicDump)); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		run(referenceSelect("SELECT id FROM timezone_control_sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(timezoneViewsAbbrevs(abbrevQuery, [][]any{{"IST", "PT5H30M", false}}))
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &redump); err != nil || redump.String() != symbolicDump {
			t.Fatal("symbolic redump changed", err, redump.String(), symbolicDump)
		}
	})
}
