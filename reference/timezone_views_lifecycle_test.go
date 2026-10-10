package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestReferenceTimezoneViewsLifecycle(t *testing.T) {
	timezoneViewsScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c referenceExecutor) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		query := "SELECT * FROM pg_timezone_abbrevs WHERE abbrev='IST'"
		held := timezoneHeld(t, p, c, query)
		check := func(offset string) {
			referenceRunStep(t, p.ctx, held, b.recorder, timezoneViewsAbbrevs(query, [][]any{{"IST", offset, false}}))
		}
		defer c.ExecContext(p.ctx, "ROLLBACK")
		for _, prepared := range []bool{false, true} {
			utility := func(q string) { s := referenceExec(q, 0); s.prepared = prepared; run(s) }
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
		run(referenceExec("BEGIN", 0))
		run(referenceSelect("SELECT set_config('timezone_abbreviations','India',true)", []string{"set_config"}, []string{"TEXT"}, [][]any{{"India"}}))
		check("05:30:00")
		run(referenceExec("ROLLBACK", 0))
		check("02:00:00")
		peer, err := b.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		for _, q := range []string{"SET TIME ZONE 'UTC'", "SET timezone_abbreviations='India'", "SET IntervalStyle='postgres'"} {
			referenceRunStep(t, p.ctx, peer, b.recorder, referenceExec(q, 0))
		}
		referenceRunStep(t, p.ctx, peer, b.recorder, timezoneViewsAbbrevs(query, [][]any{{"IST", "05:30:00", false}}))
		check("02:00:00")
		if b.pg {
			// lib/pq may complete a short precanceled query or close its
			// transport. Use the existing observed-admission/server-cancel
			// fixture, then exercise this feature on the healthy held session.
			// This is a session recovery witness, not timezone-provider latency.
			for _, prepared := range []bool{false, true} {
				referenceSessionCancel(t, p.ctx, c.(*sql.Conn), prepared, true)
				check("02:00:00")
			}
		} else {
			canceled, cancel := context.WithCancel(p.ctx)
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
			s := referenceImplicitError(tc.query, referenceDiagnostic{Code: "22023", Message: tc.message})
			s.errorStage = "execute"
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
	timezoneViewsScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c referenceExecutor) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		stamp := func() time.Time {
			rows, err := c.QueryContext(p.ctx, "SELECT CURRENT_TIMESTAMP AS stamp")
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
		run(referenceExec("BEGIN", 0))
		defer c.ExecContext(p.ctx, "ROLLBACK")
		birth := stamp()
		if birth.Year() < 2007 {
			t.Fatal("contemporary rule witness requires year >=2007")
		}
		abbrev, offset, dst := timezoneViewsNYExpected(birth)
		for _, q := range []string{"SAVEPOINT clock_mark", "SET TIME ZONE 'Asia/Kolkata'", "SET timezone_abbreviations='India'", "ROLLBACK TO clock_mark", "SET TIME ZONE 'America/New_York'"} {
			run(referenceExec(q, 0))
			run(timezoneViewsNames("SELECT * FROM pg_timezone_names WHERE name='America/New_York'", [][]any{{"America/New_York", abbrev, offset, dst}}))
		}
		referenceTemporalAdvanceClock(t, p.ctx, birth)
		if got := stamp(); !got.Equal(birth) {
			t.Fatal("transaction timestamp advanced", birth, got)
		}
		run(referenceSelect("SELECT a.utc_offset=b.utc_offset AS same FROM pg_timezone_names a JOIN pg_timezone_names b ON a.name=b.name WHERE a.name='America/New_York'", []string{"same"}, []string{"BOOL"}, [][]any{{true}}))
		run(referenceExec("COMMIT", 0))
		run(referenceExec("BEGIN", 0))
		if got := stamp(); !got.After(birth) {
			t.Fatal("new transaction did not advance", birth, got)
		}
		run(referenceExec("ROLLBACK", 0))
	})
}

func TestReferenceTimezoneViewsPublicSelect(t *testing.T) {
	timezoneViewsScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c referenceExecutor) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE ROLE timezone_view_reader", 0))
		defer c.ExecContext(p.ctx, "DROP ROLE timezone_view_reader")
		run(referenceExec("SET ROLE timezone_view_reader", 0))
		defer c.ExecContext(p.ctx, "RESET ROLE")
		run(timezoneViewsNames("SELECT * FROM pg_catalog.pg_timezone_names WHERE name='UTC'", [][]any{{"UTC", "UTC", "00:00:00", false}}))
		run(referencePrepared(timezoneViewsAbbrevs("SELECT * FROM pg_catalog.pg_timezone_abbrevs WHERE abbrev='UTC'", [][]any{{"UTC", "00:00:00", false}})))
		run(referenceExec("RESET ROLE", 0))
		run(referenceExec("DROP ROLE timezone_view_reader", 0))
	})
}
