package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONBConcatDeleteCancellation(t *testing.T) {
	jsonbConcatDeleteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{
			`SELECT $1::jsonb || $2::jsonb AS v`,
			`SELECT $1::jsonb - $2::text AS v`,
			`SELECT $1::jsonb - $2::integer AS v`,
		} {
			t.Run(q, func(t *testing.T) {
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				args, want := []any{`[1,2]`, `3`}, `[1, 2, 3]`
				if q == `SELECT $1::jsonb - $2::text AS v` {
					args, want = []any{`[1,2]`, "missing"}, `[1, 2]`
				}
				if q == `SELECT $1::jsonb - $2::integer AS v` {
					args, want = []any{`[1,2]`, int64(-1)}, `[1]`
				}
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				good := jsonbConcatDeleteSelect(q, want, args...)
				if b.PG {
					// Server-side cancellation observes admission and joins the
					// worker without retiring this physical connection. Exercise
					// the preheld feature consumer on that SAME owner afterward.
					for _, prepared := range []bool{false, true} {
						fixture.SessionCancel(t, p.Ctx, c, prepared, true)
						fixture.RunStep(t, p.Ctx, held, b.Recorder, good)
					}
				} else {
					ctx, cancel := context.WithCancel(p.Ctx)
					cancel()
					rows, err := c.QueryContext(ctx, q, args...)
					if rows != nil {
						rows.Close()
						t.Fatal("precanceled direct call published rows")
					}
					fixture.LexicalOrdinary(t, err, context.Canceled)
					rows, err = stmt.QueryContext(ctx, args...)
					if rows != nil {
						rows.Close()
						t.Fatal("precanceled held call published rows")
					}
					fixture.LexicalOrdinary(t, err, context.Canceled)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, good)
				}
			})
		}
		jsonbConcatDeleteActiveCancel(t, p, b, c)
	})
}
