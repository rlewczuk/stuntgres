package reference_test

import (
	"context"
	"database/sql"
	"testing"
)

// All spellings consume the selected computed INTERVAL result through the same
// explicit output cast. Expectations are PostgreSQL output-style constants.
func TestReferenceDatetimeArithmeticIntervalResultCasts(t *testing.T) {
	datetimeArithmeticScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		q := "SELECT ($1::INTERVAL+$2::INTERVAL)::TEXT AS postfix,CAST($1::INTERVAL+$2::INTERVAL AS TEXT) AS standard,text($1::INTERVAL+$2::INTERVAL) AS function"
		held := timezoneHeld(t, p, c, q)
		for _, tc := range []struct{ style, want string }{{"postgres", "1 day 01:00:00"}, {"postgres_verbose", "@ 1 day 1 hour"}, {"sql_standard", "1 1:00:00"}, {"iso_8601", "P1DT1H"}, {"postgres", "1 day 01:00:00"}} {
			referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("SET IntervalStyle TO '"+tc.style+"'", 0))
			s := referenceSelect(q, []string{"postfix", "standard", "function"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{tc.want, tc.want, tc.want}}, "1 day", "1 hour")
			s.checkMetadata = func(t *testing.T, rows *sql.Rows) {
				meta, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				datetimeArithmeticMetadata(t, b.pg, meta)
			}
			referenceRunStep(t, p.ctx, held, b.recorder, s)
			datetimeArithmeticBoth(t, p, b, c, s)
			s.args = []any{nil, "1 hour"}
			s.rows = [][]any{{nil, nil, nil}}
			datetimeArithmeticBoth(t, p, b, c, s)
			s.query += " WHERE FALSE"
			s.rows = nil
			datetimeArithmeticBoth(t, p, b, c, s)
		}
		if !b.pg {
			// Native controls can change settings while a materialized cursor is
			// retained; PostgreSQL's active protocol cursor has a different lease.
			capture := referenceSelect("SELECT (INTERVAL '1 day'+INTERVAL '1 hour')::TEXT AS result", []string{"result"}, []string{"TEXT"}, [][]any{{"1 day 01:00:00"}})
			rows, err := c.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, b.recorder), capture.query)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("SET IntervalStyle TO 'iso_8601'", 0))
			referenceRunStep(t, p.ctx, timezoneAdmittedRows{referenceExecutor: c, rows: rows}, b.recorder, capture)
			referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect(capture.query, capture.columns, capture.types, [][]any{{"P1DT1H"}}))
		}
	})
}
