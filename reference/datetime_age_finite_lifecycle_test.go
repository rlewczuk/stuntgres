package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

func datetimeAgeFiniteHeldParameters(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
	t.Helper()
	q := "SELECT age($1::timestamp,$2::timestamp) AS symbolic,isfinite($3::interval) AS finite"
	stmt, err := c.PrepareContext(p.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	x := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
	for _, tc := range []struct {
		args []any
		want []any
	}{
		{[]any{"2004-06-01", "2004-04-30", "1 mon -2 days"}, []any{"1 mon 1 day", true}},
		{[]any{nil, "2004-04-30", "-infinity"}, []any{nil, false}},
		{[]any{"2004-06-01", nil, nil}, []any{nil, nil}},
	} {
		s := referenceSelect(q, []string{"symbolic", "finite"}, []string{"INTERVAL", "BOOL"}, [][]any{tc.want}, tc.args...)
		s.checkMetadata = func(t *testing.T, rows *sql.Rows) {
			meta, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			datetimeArithmeticMetadata(t, b.pg, meta)
		}
		referenceRunStep(t, p.ctx, x, b.recorder, s)
	}
}

func TestReferenceDatetimeAgeFiniteMetadataPrepared(t *testing.T) {
	datetimeAgeFiniteScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, tc := range []struct {
			q    string
			rows [][]any
		}{
			{"SELECT age(TIMESTAMP(0) '2004-06-01',TIMESTAMP(6) '2004-04-30'),isfinite(INTERVAL(2) '0')", [][]any{{"1 mon 1 day", true}}},
			{"SELECT age(NULL::timestamp),isfinite(NULL::date)", [][]any{{nil, nil}}},
			{"SELECT age(NULL::timestamp),isfinite(NULL::interval) WHERE false", nil},
		} {
			t.Run(tc.q, func(t *testing.T) {
				datetimeAgeFiniteBoth(t, p, b, c, referenceSelect(tc.q, []string{"age", "isfinite"}, []string{"INTERVAL", "BOOL"}, tc.rows))
			})
		}
		datetimeAgeFiniteHeldParameters(t, p, b, c)
		// An exact TIMESTAMP argument resolves an untyped parameter to TIMESTAMP.
		datetimeAgeFiniteBoth(t, p, b, c, referenceSelect("SELECT age(TIMESTAMP '2004-06-01',$1) AS result", []string{"result"}, []string{"INTERVAL"}, [][]any{{"1 mon 1 day"}}, "2004-04-30"))
		q := "SELECT age($1::timestamp,$2::timestamp) AS result"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		x := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
		// Native retained rows survive a setting change. PostgreSQL's wire cursor
		// must be consumed before another command on the same connection.
		ctx := context.WithValue(p.ctx, referenceRecorderKey{}, b.recorder)
		rows, err := x.QueryContext(ctx, q, "2004-06-01", "2004-04-30")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		meta, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		datetimeArithmeticMetadata(t, b.pg, meta)
		if !b.pg {
			referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("SET IntervalStyle='iso_8601'", 0))
		}
		referenceRunStep(t, p.ctx, timezoneAdmittedRows{referenceExecutor: c, rows: rows}, b.recorder, referenceSelect(q, []string{"result"}, []string{"INTERVAL"}, [][]any{{"1 mon 1 day"}}, "2004-06-01", "2004-04-30"))
		datetimeArithmeticMetadata(t, b.pg, meta)
		if b.pg {
			referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("SET IntervalStyle='iso_8601'", 0))
		}
		referenceRunStep(t, p.ctx, x, b.recorder, referenceSelect(q, []string{"result"}, []string{"INTERVAL"}, [][]any{{"P1M1D"}}, "2004-06-01", "2004-04-30"))
	})
}

func TestReferenceDatetimeAgeFiniteUnaryTransaction(t *testing.T) {
	datetimeAgeFiniteScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		q := "SELECT age($1::timestamp) AS supplied,age(CURRENT_DATE::timestamp) AS midnight"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		x := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
		for round := range 2 {
			t.Run(fmt.Sprintf("transaction=%d", round), func(t *testing.T) {
				referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("BEGIN", 0))
				defer c.ExecContext(p.ctx, "ROLLBACK")
				for _, zone := range []string{"Pacific/Kiritimati", "Pacific/Pago_Pago", "UTC"} {
					referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("SET TIME ZONE '"+zone+"'", 0))
					var current string
					if err := c.QueryRowContext(p.ctx, "SELECT CURRENT_DATE::text").Scan(&current); err != nil {
						t.Fatal(err)
					}
					// The independently acquired transaction-local date is supplied as midnight;
					// no age result or external wall clock participates in the expectation.
					s := referenceSelect(q, []string{"supplied", "midnight"}, []string{"INTERVAL", "INTERVAL"}, [][]any{{"00:00:00", "00:00:00"}}, current)
					s.checkMetadata = func(t *testing.T, rows *sql.Rows) {
						meta, err := rows.ColumnTypes()
						if err != nil {
							t.Fatal(err)
						}
						datetimeArithmeticMetadata(t, b.pg, meta)
					}
					referenceRunStep(t, p.ctx, x, b.recorder, s)
					for _, tc := range []struct{ expression, want string }{
						{"age(CURRENT_DATE::timestamp)", "00:00:00"},
						{"age((CURRENT_DATE+1)::timestamp)", "-1 days"},
						{"age(CURRENT_DATE::timestamp+INTERVAL '12 hours')", "-12:00:00"},
						{"age(TIMESTAMP '2004-06-01',TIMESTAMP '2004-04-30')", "1 mon 1 day"},
					} {
						datetimeAgeFiniteValue(t, p, b, c, tc.expression, "INTERVAL", tc.want)
					}
				}
				referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("COMMIT", 0))
			})
		}
	})
}
