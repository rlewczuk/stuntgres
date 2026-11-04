package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatSubscriptionStatsReadOnly(t *testing.T) {
	statSubscriptionScenario(t, []string{"stat_sub_readonly"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		statSubscriptionCreate(t, p, b, c, "stat_sub_readonly")
		id := statSubscriptionID(t, p, c, "stat_sub_readonly")
		reset := statSubscriptionStatsReset(t, p, c, id)
		want := [][]any{statSubscriptionStatsRow(id, "stat_sub_readonly", reset)}
		statSubscriptionExec(t, p, b, c, "CREATE TABLE stat_subscription_sentinel(v integer)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO stat_subscription_sentinel VALUES(37)", 1))
		held := statSubscriptionHeld(t, p, c, statSubscriptionStatsSQL)
		defer held.Statement.Close()
		old, err := c.QueryContext(p.Ctx, statSubscriptionStatsSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		if b.PG {
			statSubscriptionStatsRead(t, old, true, want)
		}
		for _, tc := range []struct{ query, operation string }{
			{"INSERT INTO pg_catalog.pg_stat_subscription_stats(subid) VALUES(1)", "INSERT"},
			{"INSERT INTO pg_catalog.pg_stat_subscription_stats(subid) SELECT 1::oid WHERE FALSE", "INSERT"},
			{"UPDATE pg_catalog.pg_stat_subscription_stats SET apply_error_count=1", "UPDATE"},
			{"UPDATE pg_catalog.pg_stat_subscription_stats SET apply_error_count=1 WHERE FALSE", "UPDATE"},
			{"UPDATE pg_catalog.pg_stat_subscription_stats SET apply_error_count=1 WHERE NULL::boolean", "UPDATE"},
			{"DELETE FROM pg_catalog.pg_stat_subscription_stats WHERE NULL::boolean", "DELETE"},
			{"DELETE FROM pg_catalog.pg_stat_subscription_stats", "DELETE"},
			{"DELETE FROM pg_catalog.pg_stat_subscription_stats WHERE FALSE", "DELETE"},
			{"TRUNCATE pg_catalog.pg_stat_subscription_stats", "TRUNCATE"},
		} {
			for _, prepared := range []bool{false, true} {
				diagnostic := statSubscriptionStatsMutation(tc.operation)
				if tc.operation == "TRUNCATE" {
					diagnostic = fixture.ControlsReferenceDiagnostic("42809", `"pg_stat_subscription_stats" is not a table`)
				}
				step := fixture.ErrorDiagnostic(tc.query, diagnostic)
				step.Prepared, step.ErrorStage = prepared, "execute"
				if prepared && tc.operation != "TRUNCATE" {
					step.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				statSubscriptionStatsCheck(t, p, b, held, statSubscriptionStatsSQL, want)
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
		result, err := fixture.CopyFrom(p.Ctx, copyConn, b.PG, "COPY pg_catalog.pg_stat_subscription_stats(subid) FROM STDIN", strings.NewReader("1\n"))
		diagnostic := fixture.ControlsReferenceDiagnostic("42809", `cannot copy to view "pg_stat_subscription_stats"`)
		diagnostic.Hint = "To enable copying to a view, provide an INSTEAD OF INSERT trigger."
		fixture.AssertDiagnostic(t, err, &diagnostic)
		if result.Count != 0 || result.CommandTag != "" {
			t.Fatal("failed COPY returned success effects", result)
		}
		statSubscriptionStatsCheck(t, p, b, c, statSubscriptionStatsSQL, want)
		statSubscriptionStatsCheck(t, p, b, held, statSubscriptionStatsSQL, want)
		statSubscriptionSentinel(t, p, b, c)
		if !b.PG {
			statSubscriptionStatsRead(t, old, false, want)
		}
	})
}
