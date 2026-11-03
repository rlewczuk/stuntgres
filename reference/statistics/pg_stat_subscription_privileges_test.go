package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatSubscriptionPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"stat_sub_reader", "stat_sub_owner"})
	statSubscriptionScenario(t, []string{"stat_sub_protected"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, admin *sql.Conn) {
		statSubscriptionExec(t, p, b, admin, "CREATE ROLE stat_sub_reader LOGIN PASSWORD 'role-core-fixture'")
		statSubscriptionExec(t, p, b, admin, "CREATE ROLE stat_sub_owner")
		statSubscriptionExec(t, p, b, admin, "CREATE SUBSCRIPTION stat_sub_protected CONNECTION 'host=publisher.invalid password=actual_secret' PUBLICATION p WITH (connect=false,slot_name=NONE)")
		statSubscriptionExec(t, p, b, admin, "ALTER SUBSCRIPTION stat_sub_protected OWNER TO stat_sub_owner")
		id := statSubscriptionID(t, p, admin, "stat_sub_protected")
		want := [][]any{statSubscriptionRow(id, "stat_sub_protected")}
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
			defer cancel()
			for _, query := range []string{
				"GRANT SELECT ON pg_catalog.pg_stat_subscription TO PUBLIC",
				"GRANT EXECUTE ON FUNCTION pg_catalog.pg_stat_get_subscription(oid) TO PUBLIC",
				"REVOKE SELECT(subname) ON pg_catalog.pg_stat_subscription FROM stat_sub_reader",
				"REVOKE pg_monitor,pg_read_all_stats,stat_sub_owner FROM stat_sub_reader",
				"DROP SUBSCRIPTION IF EXISTS stat_sub_protected",
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
		pool, _, recorder := fixture.DatabaseScenarioPool(t, p, b.PG, database, "stat_sub_reader")
		defer pool.Close()
		actor, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer actor.Close()
		ab := fixture.Backend{Name: b.Name, DB: pool, Recorder: recorder, PG: b.PG}
		fixture.RunStep(t, p.Ctx, actor, recorder, fixture.Select("SELECT session_user,current_user", []string{"session_user", "current_user"}, []string{"NAME", "NAME"}, [][]any{{"stat_sub_reader", "stat_sub_reader"}}))
		held := statSubscriptionHeld(t, p, actor, statSubscriptionSQL)
		defer held.Statement.Close()
		check := func() {
			t.Helper()
			for _, e := range []fixture.Executor{actor, held} {
				statSubscriptionCheck(t, p, ab, e, statSubscriptionSQL, want)
			}
		}
		check() // Actual authenticated nonowner: identities are public, not masked.
		for _, role := range []string{"pg_read_all_stats", "pg_monitor", "stat_sub_owner"} {
			statSubscriptionExec(t, p, b, admin, "GRANT "+role+" TO stat_sub_reader")
			check()
			statSubscriptionExec(t, p, ab, actor, "SET ROLE "+role)
			check()
			statSubscriptionExec(t, p, ab, actor, "RESET ROLE")
			statSubscriptionExec(t, p, b, admin, "REVOKE "+role+" FROM stat_sub_reader")
			check()
		}
		for _, query := range []string{
			"SELECT subconninfo FROM pg_catalog.pg_subscription",
			"SELECT subconninfo FROM pg_catalog.pg_subscription WHERE FALSE",
			"SELECT subname FROM pg_catalog.pg_subscription WHERE subconninfo='actual_secret'",
			"SELECT pg_subscription FROM pg_catalog.pg_subscription WHERE FALSE",
		} {
			step := fixture.ErrorDiagnostic(query, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table pg_subscription"))
			step.SelectRows, step.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, actor, recorder, step)
			fixture.RunStep(t, p.Ctx, actor, recorder, fixture.Prepared(step))
		}
		undefined := fixture.ControlsReferenceDiagnostic("42703", `column "conninfo" does not exist`)
		undefined.Position = "8"
		step := fixture.ErrorDiagnostic("SELECT conninfo FROM pg_stat_subscription", undefined)
		step.SelectRows, step.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, actor, recorder, step)
		step.Prepared, step.ErrorStage = true, "prepare"
		fixture.RunStep(t, p.Ctx, actor, recorder, step)
		queries := []string{
			statSubscriptionSQL,
			statSubscriptionSQL + " WHERE FALSE",
			"SELECT subname FROM pg_stat_subscription WHERE pid IS NULL",
			"SELECT pg_stat_subscription FROM pg_stat_subscription WHERE FALSE",
		}
		var statements []fixture.HeldStatement
		for _, query := range queries {
			h := statSubscriptionHeld(t, p, actor, query)
			defer h.Statement.Close()
			statements = append(statements, h)
		}
		deny := func(query string, e fixture.Executor, diagnostic fixture.Diagnostic, args ...any) {
			t.Helper()
			step := fixture.ErrorDiagnostic(query, diagnostic, args...)
			step.SelectRows, step.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, e, recorder, step)
		}
		deniedView := fixture.ControlsReferenceDiagnostic("42501", "permission denied for view pg_stat_subscription")
		statSubscriptionExec(t, p, b, admin, "REVOKE SELECT ON pg_catalog.pg_stat_subscription FROM PUBLIC")
		for i, query := range queries {
			deny(query, actor, deniedView)
			deny(query, statements[i], deniedView)
			step := fixture.ErrorDiagnostic(query, deniedView)
			step.SelectRows, step.ErrorStage, step.Prepared = true, "execute", true
			fixture.RunStep(t, p.Ctx, actor, recorder, step)
		}
		const columnSQL = "SELECT subname FROM pg_catalog.pg_stat_subscription"
		columnHeld := statSubscriptionHeld(t, p, actor, columnSQL)
		defer columnHeld.Statement.Close()
		for _, action := range []string{"GRANT", "REVOKE", "GRANT"} {
			preposition := "TO"
			if action == "REVOKE" {
				preposition = "FROM"
			}
			statSubscriptionExec(t, p, b, admin, action+" SELECT(subname) ON pg_catalog.pg_stat_subscription "+preposition+" stat_sub_reader")
			for _, e := range []fixture.Executor{actor, columnHeld} {
				if action == "REVOKE" {
					deny(columnSQL, e, deniedView)
				} else {
					fixture.RunStep(t, p.Ctx, e, recorder, fixture.Select(columnSQL, []string{"subname"}, []string{"NAME"}, [][]any{{"stat_sub_protected"}}))
				}
			}
			// A selected-column grant does not admit hidden predicates or whole rows.
			for i, query := range queries {
				deny(query, statements[i], deniedView)
			}
		}
		statSubscriptionExec(t, p, b, admin, "GRANT SELECT ON pg_catalog.pg_stat_subscription TO PUBLIC")
		check()
		statSubscriptionExec(t, p, b, admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_stat_get_subscription(oid) FROM PUBLIC")
		deniedRoutine := fixture.ControlsReferenceDiagnostic("42501", "permission denied for function pg_stat_get_subscription")
		deny(statSubscriptionSQL, actor, deniedRoutine)
		deny(statSubscriptionSQL, held, deniedRoutine)
		deny(columnSQL, actor, deniedRoutine)
		deny(columnSQL, columnHeld, deniedRoutine)
		// Constant-false filtering never invokes the backing getter. SELECT
		// admission still applies, but unused EXECUTE authority is not demanded.
		statSubscriptionCheck(t, p, ab, actor, queries[1], nil)
		statSubscriptionCheck(t, p, ab, statements[1], queries[1], nil)
		statSubscriptionCheck(t, p, ab, actor, statSubscriptionSQL+" WHERE NULL::boolean", nil)
		deny(statSubscriptionSQL+" WHERE TRUE", actor, deniedRoutine)
		predicateSQL := statSubscriptionSQL + " WHERE $1"
		predicateHeld := statSubscriptionHeld(t, p, actor, predicateSQL)
		defer predicateHeld.Statement.Close()
		for _, executor := range []fixture.Executor{actor, predicateHeld} {
			for _, argument := range []any{false, nil, false} {
				statSubscriptionCheck(t, p, ab, executor, predicateSQL, nil, argument)
				// The same held plan must re-demand the getter for TRUE rather
				// than retain an earlier FALSE/NULL decision or old authority.
				deny(predicateSQL, executor, deniedRoutine, true)
			}
		}
		statSubscriptionExec(t, p, b, admin, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_stat_get_subscription(oid) TO PUBLIC")
		check()
		statSubscriptionCheck(t, p, ab, statements[1], queries[1], nil)
	})
}
