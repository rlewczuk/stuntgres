package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatSubscriptionStatsFeatureSmoke(t *testing.T) {
	statSubscriptionScenario(t, []string{"stat_subscription_stats_smoke", "stat_subscription_stats_smoke_renamed"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		statSubscriptionExec(t, p, b, c, "CREATE SUBSCRIPTION stat_subscription_stats_smoke CONNECTION 'host=publisher.invalid password=actual_secret' PUBLICATION p WITH (connect=false,slot_name=NONE)")
		id := statSubscriptionID(t, p, c, "stat_subscription_stats_smoke")
		query := statSubscriptionStatsSQL + " WHERE subid=$1::oid"
		held := statSubscriptionHeld(t, p, c, query)
		defer held.Statement.Close()
		want := [][]any{statSubscriptionStatsRow(id, "stat_subscription_stats_smoke")}
		for _, e := range []fixture.Executor{c, held, held} {
			statSubscriptionStatsCheck(t, p, b, e, query, want, id)
		}
		statSubscriptionCheck(t, p, b, c, statSubscriptionSQL+" WHERE subid=$1::oid", [][]any{statSubscriptionRow(id, "stat_subscription_stats_smoke")}, id)
		statSubscriptionExec(t, p, b, c, "CREATE TABLE stat_subscription_sentinel(v integer)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO stat_subscription_sentinel VALUES(37)", 1))
		step := fixture.ErrorDiagnostic("UPDATE pg_catalog.pg_stat_subscription_stats SET apply_error_count=37", statSubscriptionStatsMutation("UPDATE"))
		step.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
		step.Prepared, step.ErrorStage = true, "prepare"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
		statSubscriptionSentinel(t, p, b, c)
		reset := statSubscriptionStatsReset(t, p, c, id)
		want[0] = statSubscriptionStatsRow(id, "stat_subscription_stats_smoke", reset)
		statSubscriptionStatsCheck(t, p, b, held, query, want, id)
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		expired, expire := context.WithDeadline(p.Ctx, time.Unix(1, 0))
		defer expire()
		executors := []fixture.Executor{c, held}
		if b.PG {
			// Borrowed pq connections race fast completion against their
			// cancellation watcher. Pool checkout provides the deterministic
			// pre-admission context boundary without exposing that race.
			executors = []fixture.Executor{b.DB}
		}
		for _, tc := range []struct {
			ctx context.Context
			err error
		}{{canceled, context.Canceled}, {expired, context.DeadlineExceeded}} {
			for _, e := range executors {
				rows, err := e.QueryContext(tc.ctx, query, id)
				if rows != nil {
					rows.Close()
					t.Fatal("pre-canceled query published rows")
				}
				if !errors.Is(err, tc.err) {
					t.Fatal("ordinary context boundary", err, tc.err)
				}
				var state interface{ SQLState() string }
				if errors.As(err, &state) {
					t.Fatal("ordinary context error acquired fabricated SQLSTATE", err)
				}
			}
		}
		statSubscriptionStatsCheck(t, p, b, held, query, want, id)
		statSubscriptionSentinel(t, p, b, c)
		statSubscriptionExec(t, p, b, c, "ALTER SUBSCRIPTION stat_subscription_stats_smoke RENAME TO stat_subscription_stats_smoke_renamed")
		if renamed := statSubscriptionID(t, p, c, "stat_subscription_stats_smoke_renamed"); renamed != id {
			t.Fatal("rename changed OID", renamed, id)
		}
		want[0] = statSubscriptionStatsRow(id, "stat_subscription_stats_smoke_renamed", reset)
		statSubscriptionStatsCheck(t, p, b, held, query, want, id)
		statSubscriptionExec(t, p, b, c, "DROP SUBSCRIPTION stat_subscription_stats_smoke_renamed")
		statSubscriptionStatsCheck(t, p, b, held, query, nil, id)
		statSubscriptionStatsCheck(t, p, b, c, query, nil, id)
		statSubscriptionSentinel(t, p, b, c)
	})
}
