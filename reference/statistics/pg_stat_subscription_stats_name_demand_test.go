package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatSubscriptionStatsNameFilteredGetterDemand(t *testing.T) {
	for _, tc := range []struct {
		name      string
		predicate string
		parameter bool
	}{
		{"literal_false", "FALSE", false},
		{"literal_null", "NULL::boolean", false},
		{"parameter", "$2", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const role = "stat_sub_stats_demand_reader"
			const name = "stat_sub_stats_name_demand"
			fixture.RoleReferenceCleanup(t, []string{role})
			statSubscriptionScenario(t, []string{name}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, admin *sql.Conn) {
				statSubscriptionExec(t, p, b, admin, "CREATE ROLE "+role+" LOGIN PASSWORD 'role-core-fixture'")
				statSubscriptionCreate(t, p, b, admin, name)
				id := statSubscriptionID(t, p, admin, name)
				reset := statSubscriptionStatsReset(t, p, admin, id)
				want := [][]any{statSubscriptionStatsRow(id, name, reset)}
				defer func() {
					ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
					defer cancel()
					for _, query := range []string{
						"GRANT SELECT ON pg_catalog.pg_stat_subscription_stats TO PUBLIC",
						"GRANT EXECUTE ON FUNCTION pg_catalog.pg_stat_get_subscription_stats(oid) TO PUBLIC",
						"DROP SUBSCRIPTION IF EXISTS " + name,
					} {
						if _, err := admin.ExecContext(ctx, query); err != nil {
							t.Error("restoring shared authority", query, err)
						}
					}
				}()
				var database string
				if err := admin.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
					t.Fatal(err)
				}
				pool, _, recorder := fixture.DatabaseScenarioPool(t, p, b.PG, database, role)
				defer pool.Close()
				actor, err := pool.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer actor.Close()
				ab := fixture.Backend{Name: b.Name, DB: pool, Recorder: recorder, PG: b.PG}
				if tc.parameter {
					// Repeated executions exercise custom-plan parameter demand,
					// not PostgreSQL's cost-based switch to a generic plan.
					statSubscriptionExec(t, p, ab, actor, "SET plan_cache_mode=force_custom_plan")
					defer func() {
						ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
						defer cancel()
						if _, err := actor.ExecContext(ctx, "RESET plan_cache_mode"); err != nil {
							t.Error("restoring actor plan cache mode", err)
						}
					}()
					var mode string
					if err := actor.QueryRowContext(p.Ctx, "SHOW plan_cache_mode").Scan(&mode); err != nil || mode != "force_custom_plan" {
						t.Fatal("actual actor custom-plan policy", mode, err)
					}
				}
				query := statSubscriptionStatsSQL + " WHERE subname=$1 AND " + tc.predicate
				held := statSubscriptionHeld(t, p, actor, query)
				defer held.Statement.Close()
				arguments := []any{name}
				var initial [][]any
				if tc.parameter {
					arguments = append(arguments, true)
					initial = want
				}
				for _, executor := range []fixture.Executor{actor, held} {
					statSubscriptionStatsCheck(t, p, ab, executor, query, initial, arguments...)
				}
				deny := func(t *testing.T, executor fixture.Executor, diagnostic fixture.Diagnostic, args ...any) {
					t.Helper()
					step := fixture.ErrorDiagnostic(query, diagnostic, args...)
					step.SelectRows, step.ErrorStage = true, "execute"
					fixture.RunStep(t, p.Ctx, executor, recorder, step)
				}
				statSubscriptionExec(t, p, b, admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_stat_get_subscription_stats(oid) FROM PUBLIC")
				deniedRoutine := fixture.ControlsReferenceDiagnostic("42501", "permission denied for function pg_stat_get_subscription_stats")
				for i, executor := range []fixture.Executor{actor, held} {
					t.Run([]string{"direct", "held"}[i], func(t *testing.T) {
						// A rejected conjunction must not demand the backing getter;
						// TRUE on the same held definition must recheck current ACLs.
						if tc.parameter {
							for _, argument := range []any{false, nil, false} {
								statSubscriptionStatsCheck(t, p, ab, executor, query, nil, name, argument)
								deny(t, executor, deniedRoutine, name, true)
							}
						} else {
							statSubscriptionStatsCheck(t, p, ab, executor, query, nil, name)
						}
					})
				}
				statSubscriptionExec(t, p, b, admin, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_stat_get_subscription_stats(oid) TO PUBLIC")
				for _, executor := range []fixture.Executor{actor, held} {
					statSubscriptionStatsCheck(t, p, ab, executor, query, initial, arguments...)
				}
				// SELECT remains mandatory even when the getter is unused.
				statSubscriptionExec(t, p, b, admin, "REVOKE SELECT ON pg_catalog.pg_stat_subscription_stats FROM PUBLIC")
				deniedView := fixture.ControlsReferenceDiagnostic("42501", "permission denied for view pg_stat_subscription_stats")
				for _, executor := range []fixture.Executor{actor, held} {
					if tc.parameter {
						for _, argument := range []any{false, nil, true} {
							deny(t, executor, deniedView, name, argument)
						}
					} else {
						deny(t, executor, deniedView, name)
					}
				}
			})
		})
	}
}
