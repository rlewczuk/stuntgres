package reference_test

import (
	"context"
	"errors"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceInfoKeyColumnUsageDiagnostics(t *testing.T) {
	kcuScenario(t, func(c *kcuCase) {
		c.exec("CREATE TABLE kcu_diag(a integer CONSTRAINT kcu_diag_p PRIMARY KEY)")
		held := c.hold(c.conn, kcuQuery)
		for _, q := range []string{"SELECT missing FROM information_schema.key_column_usage", "/* éλ */ SELECT missing FROM information_schema.key_column_usage"} {
			want := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
			want.Position = fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, "missing")]) + 1)
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(q, want)
				s.SelectRows = true
				s.ErrorStage = "execute"
				if prepared {
					s = fixture.Prepared(s)
					s.ErrorStage = "prepare"
				}
				c.run(c.conn, s)
				c.check(c.conn, "kcu_diag", c.pub("kcu_diag_p", "kcu_diag", "a", 1, nil))
				c.check(held, "kcu_diag", c.pub("kcu_diag_p", "kcu_diag", "a", 1, nil))
			}
		}
		if !c.b.PG {
			ctx, cancel := context.WithCancel(c.p.Ctx)
			cancel()
			for _, e := range []fixture.Executor{c.conn, held} {
				rows, err := e.QueryContext(ctx, kcuQuery, "kcu_diag")
				if rows != nil {
					rows.Close()
					t.Fatal("canceled read exposed rows")
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatal("cancellation", err)
				}
				var state interface{ SQLState() string }
				if errors.As(err, &state) {
					t.Fatal("ordinary cancellation has SQLSTATE", state.SQLState())
				}
			}
		} else {
			fixture.SessionCancel(t, c.p.Ctx, c.conn, false, true)
			c.check(held, "kcu_diag", c.pub("kcu_diag_p", "kcu_diag", "a", 1, nil))
		}
	})
}
