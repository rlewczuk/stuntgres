package reference_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoConstraintColumnUsageDiagnostics(t *testing.T) {
	ccuScenario(t, func(c *ccuCase) {
		c.exec("CREATE TABLE ccu_diag(a integer CONSTRAINT ccu_diag_u UNIQUE)")
		held := c.hold(c.conn, ccuQuery)
		for _, test := range []struct {
			query    string
			position int
		}{
			{"SELECT missing FROM information_schema.constraint_column_usage", 8},
			{"/* éλ */ SELECT missing FROM information_schema.constraint_column_usage", 17},
		} {
			want := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
			want.Position = fixture.Position(test.position)
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(test.query, want)
				s.SelectRows = true
				s.ErrorStage = "execute"
				if prepared {
					s = fixture.Prepared(s)
					s.ErrorStage = "prepare"
				}
				c.run(c.conn, s)
				c.check(c.conn, "ccu_diag", c.pub("ccu_diag", "a", "ccu_diag_u"))
				c.check(held, "ccu_diag", c.pub("ccu_diag", "a", "ccu_diag_u"))
			}
		}
		if !c.b.PG {
			ctx, cancel := context.WithCancel(c.p.Ctx)
			cancel()
			for _, e := range []fixture.Executor{c.conn, held} {
				rows, err := e.QueryContext(ctx, ccuQuery, "ccu_diag")
				if rows != nil {
					rows.Close()
					c.t.Fatal("canceled read exposed rows")
				}
				if !errors.Is(err, context.Canceled) {
					c.t.Fatal("cancellation", err)
				}
				var state interface{ SQLState() string }
				if errors.As(err, &state) {
					c.t.Fatal("ordinary cancellation has SQLSTATE", state.SQLState())
				}
			}
		} else {
			fixture.SessionCancel(c.t, c.p.Ctx, c.conn, false, true)
		}
		c.check(c.conn, "ccu_diag", c.pub("ccu_diag", "a", "ccu_diag_u"))
		c.check(held, "ccu_diag", c.pub("ccu_diag", "a", "ccu_diag_u"))
	})
}
