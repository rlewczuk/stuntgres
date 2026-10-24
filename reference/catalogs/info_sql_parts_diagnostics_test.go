package reference_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoSQLPartsDiagnostics(t *testing.T) {
	spScenario(t, true, func(c *spCase) {
		want := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
		want.Position = "8"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT missing FROM "+spTable, want)
			s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			c.run(c.conn, s)
		}
		held := c.hold(spQuery)
		// Pre-canceled native calls are ordinary Go errors, not pq probes.
		// This phase is deliberately not exercised during PG discovery.
		if !c.b.PG {
			ctx, cancel := context.WithCancel(c.p.Ctx)
			cancel()
			for _, e := range []fixture.Executor{c.conn, held} {
				rows, err := e.QueryContext(ctx, spQuery)
				if rows != nil {
					rows.Close()
					c.t.Fatal("canceled query returned rows")
				}
				if !errors.Is(err, context.Canceled) {
					c.t.Fatalf("canceled query: %v", err)
				}
				var state interface{ SQLState() string }
				if errors.As(err, &state) {
					c.t.Fatalf("ordinary cancellation has SQLSTATE %s", state.SQLState())
				}
			}
		}
		c.run(held, c.projection(spQuery, spRows()))
		c.exec("CREATE ROLE sp_reader")
		spDefaultDenial(c)
	})
}
