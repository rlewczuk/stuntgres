package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatSubscriptionReadOnly(t *testing.T) {
	statSubscriptionScenario(t, []string{"stat_sub_readonly"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		statSubscriptionCreate(t, p, b, c, "stat_sub_readonly")
		id := statSubscriptionID(t, p, c, "stat_sub_readonly")
		want := [][]any{statSubscriptionRow(id, "stat_sub_readonly")}
		statSubscriptionExec(t, p, b, c, "CREATE TABLE stat_subscription_sentinel(v integer)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO stat_subscription_sentinel VALUES(37)", 1))
		held := statSubscriptionHeld(t, p, c, statSubscriptionSQL)
		defer held.Statement.Close()
		old, err := c.QueryContext(p.Ctx, statSubscriptionSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		if b.PG {
			statSubscriptionRead(t, old, true, want)
		}
		for _, tc := range []struct{ query, operation string }{
			{"INSERT INTO pg_catalog.pg_stat_subscription(subid) VALUES(1)", "INSERT"},
			{"INSERT INTO pg_catalog.pg_stat_subscription(subid) SELECT 1::oid WHERE FALSE", "INSERT"},
			{"UPDATE pg_catalog.pg_stat_subscription SET pid=1", "UPDATE"},
			{"UPDATE pg_catalog.pg_stat_subscription SET pid=1 WHERE FALSE", "UPDATE"},
			{"DELETE FROM pg_catalog.pg_stat_subscription", "DELETE"},
			{"DELETE FROM pg_catalog.pg_stat_subscription WHERE FALSE", "DELETE"},
			{"TRUNCATE pg_catalog.pg_stat_subscription", "TRUNCATE"},
		} {
			for _, prepared := range []bool{false, true} {
				diagnostic := statSubscriptionMutation(tc.operation)
				if tc.operation == "TRUNCATE" {
					diagnostic = fixture.ControlsReferenceDiagnostic("42809", `"pg_stat_subscription" is not a table`)
				}
				step := fixture.ErrorDiagnostic(tc.query, diagnostic)
				step.Prepared, step.ErrorStage = prepared, "execute"
				if prepared && tc.operation != "TRUNCATE" {
					step.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				statSubscriptionCheck(t, p, b, held, statSubscriptionSQL, want)
				statSubscriptionSentinel(t, p, b, c)
			}
		}
		pool := p.Native
		if b.PG {
			pool = p.CopyPostgres
		}
		copyConn, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer copyConn.Close()
		result, err := fixture.CopyFrom(p.Ctx, copyConn, b.PG, "COPY pg_catalog.pg_stat_subscription(subid) FROM STDIN", strings.NewReader("1\n"))
		diagnostic := fixture.ControlsReferenceDiagnostic("42809", `cannot copy to view "pg_stat_subscription"`)
		diagnostic.Hint = "To enable copying to a view, provide an INSTEAD OF INSERT trigger."
		fixture.AssertDiagnostic(t, err, &diagnostic)
		if result.Count != 0 || result.CommandTag != "" {
			t.Fatal("failed COPY returned success effects", result)
		}
		statSubscriptionCheck(t, p, b, c, statSubscriptionSQL, want)
		statSubscriptionCheck(t, p, b, held, statSubscriptionSQL, want)
		statSubscriptionSentinel(t, p, b, c)
		if !b.PG {
			statSubscriptionRead(t, old, false, want)
		}
	})
}
