package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// This executes the new behavior itself; it does not invoke other test functions.
func TestReferenceDatetimeArithmeticFeatureSmoke(t *testing.T) {
	datetimeArithmeticScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ expr, typ, want string }{
			{"DATE '2024-02-28'+1", "DATE", "2024-02-29"},
			{"DATE '2024-03-01'-DATE '2024-02-28'", "INT4", "2"},
			{"DATE '2024-01-31'+INTERVAL '1 month'", "TIMESTAMP", "2024-02-29 00:00:00"},
			{"TIME '23:00'+INTERVAL '3 hours'", "TIME", "02:00:00"},
			{"TIME '01:00'-TIME '23:00'", "INTERVAL", "-22:00:00"},
			{"TIMETZ '23:00+02'+INTERVAL '3 hours'", "TIMETZ", "02:00:00+02"},
			{"INTERVAL '1 month'/4", "INTERVAL", "7 days 12:00:00"},
			{"3.5::DOUBLE PRECISION*INTERVAL '1 hour'", "INTERVAL", "03:30:00"},
			{"TIME '24:00'+INTERVAL '9223372036854775807 microseconds'", "TIME", "19:59:05.224191"},
		} {
			datetimeArithmeticValue(t, p, b, c, tc.expr, tc.typ, tc.want)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET TIME ZONE 'America/Denver'", 0))
		datetimeArithmeticValue(t, p, b, c, "TIMESTAMPTZ '2005-04-02 12:00-07'+INTERVAL '1 day'", "TIMESTAMPTZ", "2005-04-03 12:00:00-06")
		datetimeArithmeticValue(t, p, b, c, "TIMESTAMPTZ '2005-04-02 12:00-07'+INTERVAL '24 hours'", "TIMESTAMPTZ", "2005-04-03 13:00:00-06")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET TIME ZONE 'America/New_York'", 0))
		datetimeArithmeticValue(t, p, b, c, "TIMESTAMPTZ '2018-02-11 02:30-05'+INTERVAL '1 month 1 day'", "TIMESTAMPTZ", "2018-03-12 03:30:00-04")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET TIME ZONE 'Etc/GMT+1'", 0))
		boundary := fixture.Select("SELECT ($1::TIMESTAMPTZ-INTERVAL '1 day') AS result,(($1::TIMESTAMPTZ-INTERVAL '1 day') AT TIME ZONE 'UTC')::TEXT AS rendered",
			[]string{"result", "rendered"}, []string{"TIMESTAMPTZ", "TEXT"},
			[][]any{{time.Date(-4713, time.November, 24, 0, 30, 0, 0, time.UTC), "4714-11-24 00:30:00 BC"}}, "4714-11-25 00:30:00+00 BC")
		datetimeArithmeticContractBoth(t, p, b, c, boundary)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET TIME ZONE 'America/Denver'", 0))
		want := fixture.ControlsReferenceDiagnostic("42883", "operator does not exist: time with time zone - time with time zone")
		want.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
		want.Position = "26"
		s := fixture.ErrorDiagnostic("SELECT TIMETZ '01:00+00' - TIMETZ '00:00+00'", want)
		s.SelectRows = true
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		s.ErrorStage = "prepare"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		s = fixture.Select("SELECT NULL::INTERVAL/0 AS result", []string{"result"}, []string{"INTERVAL"}, [][]any{{nil}})
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			m, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			datetimeArithmeticMetadata(t, b.PG, m)
		}
		datetimeArithmeticBoth(t, p, b, c, s)
		s.Query += " WHERE FALSE"
		s.Rows = nil
		datetimeArithmeticBoth(t, p, b, c, s)
		datetimeArithmeticRuntimeError(t, p, b, c, "SELECT $1::INTERVAL/$2::DOUBLE PRECISION", "22012", "division by zero", "1 hour", 0)
		datetimeArithmeticZoneComparison(t, p, b, c)
		q := "SELECT (TIMESTAMPTZ '2005-04-02 12:00-07'+INTERVAL '1 day')::TEXT AS result"
		h := timezoneHeld(t, p, c, q)
		for _, tc := range []struct{ zone, want string }{{"America/Denver", "2005-04-03 12:00:00-06"}, {"UTC", "2005-04-03 19:00:00+00"}} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET TIME ZONE '"+tc.zone+"'", 0))
			fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.Select(q, []string{"result"}, []string{"TEXT"}, [][]any{{tc.want}}))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE dt_smoke_index(z TIMESTAMPTZ)", 0))
		for _, tc := range []struct{ q, message string }{
			{"CREATE TABLE dt_smoke_generated(z TIMESTAMPTZ,g TIMESTAMPTZ GENERATED ALWAYS AS (z+INTERVAL '0') STORED)", "generation expression is not immutable"},
			{"CREATE INDEX dt_smoke_stable_idx ON dt_smoke_index ((z+INTERVAL '0'))", "functions in index expression must be marked IMMUTABLE"},
		} {
			for _, prepared := range []bool{false, true} {
				step := fixture.ErrorDiagnostic(tc.q, fixture.ControlsReferenceDiagnostic("42P17", tc.message))
				step.Prepared = prepared
				step.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			}
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP TABLE dt_smoke_index", 0))
		datetimeArithmeticStoredSeed(t, p, b, c)
		datetimeArithmeticStoredControls(t, p, b, c)
	})
}
