package reference_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoConstraintTableUsageDiagnostics(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ctu_diag_reader"})
	ctuScenario(t, func(c *ctuCase) {
		c.exec("CREATE TABLE ctu_diag(a integer CONSTRAINT ctu_diag_u UNIQUE)")
		held := c.hold(c.conn, ctuQuery)
		for _, test := range []struct {
			query    string
			position int
		}{
			{"SELECT missing FROM information_schema.constraint_table_usage", 8},
			{"/* éλ */ SELECT missing FROM information_schema.constraint_table_usage", 17},
		} {
			want := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
			want.Position = fixture.Position(test.position)
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(test.query, want)
				s.SelectRows, s.ErrorStage = true, "execute"
				if prepared {
					s = fixture.Prepared(s)
					s.ErrorStage = "prepare"
				}
				c.run(c.conn, s)
				c.routes(held, "ctu_diag", c.pub("ctu_diag", "ctu_diag_u"))
			}
		}
		c.exec("CREATE ROLE ctu_diag_reader", "GRANT USAGE ON SCHEMA public TO ctu_diag_reader", "REVOKE ALL ON ctu_diag FROM PUBLIC", "SET ROLE ctu_diag_reader")
		denied := fixture.ErrorDiagnostic("SELECT * FROM public.ctu_diag", fixture.ControlsReferenceDiagnostic("42501", "permission denied for table ctu_diag"))
		denied.SelectRows, denied.ErrorStage = true, "execute"
		c.run(c.conn, denied)
		c.run(c.conn, fixture.Prepared(denied))
		c.routes(held, "ctu_diag")
		c.exec("RESET ROLE")
		c.routes(held, "ctu_diag", c.pub("ctu_diag", "ctu_diag_u"))
	})
}

func TestReferenceInfoConstraintTableUsageCancellation(t *testing.T) {
	ctuScenario(t, func(c *ctuCase) {
		c.exec("CREATE TABLE ctu_cancel(a integer CONSTRAINT ctu_cancel_u UNIQUE)")
		held := c.hold(c.conn, ctuQuery)
		c.routes(held, "ctu_cancel", c.pub("ctu_cancel", "ctu_cancel_u"))
		// database/sql rejects an interrupted pool acquisition before calling pq.
		// A borrowed pq Conn or held statement has no deterministic fast-query
		// cancellation outcome; keep those ordinary context-error assertions native.
		executors := []fixture.Executor{c.b.DB}
		if !c.b.PG {
			executors = append(executors, c.conn, held)
		}
		for _, expired := range []bool{false, true} {
			ctx, cancel := context.WithCancel(c.p.Ctx)
			want := context.Canceled
			if expired {
				cancel()
				ctx, cancel = context.WithDeadline(c.p.Ctx, time.Unix(0, 0))
				want = context.DeadlineExceeded
			} else {
				cancel()
			}
			for _, e := range executors {
				rows, err := e.QueryContext(ctx, ctuQuery, "ctu_cancel")
				if rows != nil {
					rows.Close()
					c.t.Fatal("interrupted read exposed rows")
				}
				if !errors.Is(err, want) {
					c.t.Fatal("context error", err, "want", want)
				}
				var state interface{ SQLState() string }
				if errors.As(err, &state) {
					c.t.Fatal("ordinary cancellation has SQLSTATE", state.SQLState())
				}
			}
			cancel()
		}
		c.routes(held, "ctu_cancel", c.pub("ctu_cancel", "ctu_cancel_u"))
	})
}
