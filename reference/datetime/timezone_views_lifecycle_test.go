package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceTimezoneViewsLifecycle(t *testing.T) {
	timezoneViewsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		query := "SELECT * FROM pg_timezone_abbrevs WHERE abbrev='IST'"
		held := timezoneHeld(t, p, c, query)
		check := func(offset string) {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, timezoneViewsAbbrevs(query, [][]any{{"IST", offset, false}}))
		}
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		for _, prepared := range []bool{false, true} {
			utility := func(q string) { s := fixture.Exec(q, 0); s.Prepared = prepared; run(s) }
			utility("SET TIME ZONE 'UTC'")
			utility("SET timezone_abbreviations='Default'")
			for _, q := range []string{"BEGIN", "SET LOCAL timezone_abbreviations='India'", "SAVEPOINT mark", "SET timezone_abbreviations='Australia'", "ROLLBACK TO mark"} {
				utility(q)
			}
			check("05:30:00")
			utility("COMMIT")
			check("02:00:00")
			for _, q := range []string{"BEGIN", "SET timezone_abbreviations='India'", "SET LOCAL timezone_abbreviations='Australia'"} {
				utility(q)
			}
			check("02:00:00")
			utility("COMMIT")
			check("05:30:00")
			for _, q := range []string{"BEGIN", "SET timezone_abbreviations='Australia'", "ROLLBACK"} {
				utility(q)
			}
			check("05:30:00")
			utility("RESET timezone_abbreviations")
			check("02:00:00")
		}
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Select("SELECT set_config('timezone_abbreviations','India',true)", []string{"set_config"}, []string{"TEXT"}, [][]any{{"India"}}))
		check("05:30:00")
		run(fixture.Exec("ROLLBACK", 0))
		check("02:00:00")
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		for _, q := range []string{"SET TIME ZONE 'UTC'", "SET timezone_abbreviations='India'", "SET IntervalStyle='postgres'"} {
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec(q, 0))
		}
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, timezoneViewsAbbrevs(query, [][]any{{"IST", "05:30:00", false}}))
		check("02:00:00")
		if b.PG {
			// lib/pq may complete a short precanceled query or close its
			// transport. Use the existing observed-admission/server-cancel
			// fixture, then exercise this feature on the healthy held session.
			// This is a session recovery witness, not timezone-provider latency.
			for _, prepared := range []bool{false, true} {
				fixture.SessionCancel(t, p.Ctx, c.(*sql.Conn), prepared, true)
				check("02:00:00")
			}
		} else {
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := c.QueryContext(canceled, "SELECT * FROM pg_timezone_names")
			if rows != nil {
				rows.Close()
				t.Fatal("canceled native query exposed rows")
			}
			var state interface{ SQLState() string }
			if !errors.Is(err, context.Canceled) || errors.As(err, &state) {
				t.Fatalf("ordinary native cancellation without SQLSTATE: %v", err)
			}
			check("02:00:00")
		}
		for _, tc := range []struct{ query, message string }{
			{"SET timezone_abbreviations='default'", `invalid value for parameter "timezone_abbreviations": "default"`},
			{"SET TIME ZONE 'No/Such_Zone'", `invalid value for parameter "TimeZone": "No/Such_Zone"`},
		} {
			s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "22023", Message: tc.message})
			s.ErrorStage = "execute"
			run(s)
			check("02:00:00")
		}
	})
}

// Independent contemporary US rule, not the production provider or a second
// backend's rows. Used only for the observed transaction instant's civil year.
func timezoneViewsNYExpected(stamp time.Time) (string, string, bool) {
	year := stamp.UTC().Year()
	march := time.Date(year, 3, 1, 7, 0, 0, 0, time.UTC)
	start := march.AddDate(0, 0, (7-int(march.Weekday()))%7+7)
	november := time.Date(year, 11, 1, 6, 0, 0, 0, time.UTC)
	end := november.AddDate(0, 0, (7-int(november.Weekday()))%7)
	if !stamp.Before(start) && stamp.Before(end) {
		return "EDT", "-04:00:00", true
	}
	return "EST", "-05:00:00", false
}

func TestReferenceTimezoneViewsTransactionInstant(t *testing.T) {
	timezoneViewsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		stamp := func() time.Time {
			rows, err := c.QueryContext(p.Ctx, "SELECT CURRENT_TIMESTAMP AS stamp")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			if !rows.Next() {
				t.Fatal(rows.Err())
			}
			var value time.Time
			if err := rows.Scan(&value); err != nil {
				t.Fatal(err)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal(rows.Err())
			}
			return value.UTC()
		}
		run(fixture.Exec("BEGIN", 0))
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		birth := stamp()
		if birth.Year() < 2007 {
			t.Fatal("contemporary rule witness requires year >=2007")
		}
		abbrev, offset, dst := timezoneViewsNYExpected(birth)
		for _, q := range []string{"SAVEPOINT clock_mark", "SET TIME ZONE 'Asia/Kolkata'", "SET timezone_abbreviations='India'", "ROLLBACK TO clock_mark", "SET TIME ZONE 'America/New_York'"} {
			run(fixture.Exec(q, 0))
			run(timezoneViewsNames("SELECT * FROM pg_timezone_names WHERE name='America/New_York'", [][]any{{"America/New_York", abbrev, offset, dst}}))
		}
		fixture.TemporalAdvanceClock(t, p.Ctx, birth)
		if got := stamp(); !got.Equal(birth) {
			t.Fatal("transaction timestamp advanced", birth, got)
		}
		run(fixture.Select("SELECT a.utc_offset=b.utc_offset AS same FROM pg_timezone_names a JOIN pg_timezone_names b ON a.name=b.name WHERE a.name='America/New_York'", []string{"same"}, []string{"BOOL"}, [][]any{{true}}))
		run(fixture.Exec("COMMIT", 0))
		run(fixture.Exec("BEGIN", 0))
		if got := stamp(); !got.After(birth) {
			t.Fatal("new transaction did not advance", birth, got)
		}
		run(fixture.Exec("ROLLBACK", 0))
	})
}

func TestReferenceTimezoneViewsPublicSelect(t *testing.T) {
	timezoneViewsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE ROLE timezone_view_reader", 0))
		defer c.ExecContext(p.Ctx, "DROP ROLE timezone_view_reader")
		run(fixture.Exec("SET ROLE timezone_view_reader", 0))
		defer c.ExecContext(p.Ctx, "RESET ROLE")
		run(timezoneViewsNames("SELECT * FROM pg_catalog.pg_timezone_names WHERE name='UTC'", [][]any{{"UTC", "UTC", "00:00:00", false}}))
		run(fixture.Prepared(timezoneViewsAbbrevs("SELECT * FROM pg_catalog.pg_timezone_abbrevs WHERE abbrev='UTC'", [][]any{{"UTC", "00:00:00", false}})))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("DROP ROLE timezone_view_reader", 0))
	})
}
