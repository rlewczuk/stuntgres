package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// All spellings consume the selected computed INTERVAL result through the same
// explicit output cast. Expectations are PostgreSQL output-style constants.
func TestReferenceDatetimeArithmeticIntervalResultCasts(t *testing.T) {
	datetimeArithmeticScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT ($1::INTERVAL+$2::INTERVAL)::TEXT AS postfix,CAST($1::INTERVAL+$2::INTERVAL AS TEXT) AS standard,text($1::INTERVAL+$2::INTERVAL) AS function"
		held := timezoneHeld(t, p, c, q)
		for _, tc := range []struct{ style, want string }{{"postgres", "1 day 01:00:00"}, {"postgres_verbose", "@ 1 day 1 hour"}, {"sql_standard", "1 1:00:00"}, {"iso_8601", "P1DT1H"}, {"postgres", "1 day 01:00:00"}} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET IntervalStyle TO '"+tc.style+"'", 0))
			s := fixture.Select(q, []string{"postfix", "standard", "function"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{tc.want, tc.want, tc.want}}, "1 day", "1 hour")
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				meta, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				datetimeArithmeticMetadata(t, b.PG, meta)
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			datetimeArithmeticBoth(t, p, b, c, s)
			s.Args = []any{nil, "1 hour"}
			s.Rows = [][]any{{nil, nil, nil}}
			datetimeArithmeticBoth(t, p, b, c, s)
			s.Query += " WHERE FALSE"
			s.Rows = nil
			datetimeArithmeticBoth(t, p, b, c, s)
		}
		if !b.PG {
			// Native controls can change settings while a materialized cursor is
			// retained; PostgreSQL's active protocol cursor has a different lease.
			capture := fixture.Select("SELECT (INTERVAL '1 day'+INTERVAL '1 hour')::TEXT AS result", []string{"result"}, []string{"TEXT"}, [][]any{{"1 day 01:00:00"}})
			rows, err := c.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder), capture.Query)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET IntervalStyle TO 'iso_8601'", 0))
			fixture.RunStep(t, p.Ctx, timezoneAdmittedRows{Executor: c, rows: rows}, b.Recorder, capture)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(capture.Query, capture.Columns, capture.Types, [][]any{{"P1DT1H"}}))
		}
	})
}
