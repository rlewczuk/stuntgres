package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// REL_18_6 strict simplification may erase a custom-plan known NULL before
// EXECUTE admission. A generic parameter preserves invocation demand instead.
func TestReferenceStatsResetDatabaseParameterPrivileges(t *testing.T) {
	for _, name := range []string{"pg_stat_reset_single_table_counters", "pg_stat_reset_single_function_counters"} {
		for _, mode := range []string{"force_custom_plan", "force_generic_plan"} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				fixture.RoleReferenceCleanup(t, []string{"reset_parameter_reader"})
				activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					statsCreate(t, p, b, c, "reset_parameter_sentinel")
					statsPublish(t, p, b, "reset_parameter_sentinel", 5, fixture.Exec("INSERT INTO reset_parameter_sentinel VALUES(1),(2),(3),(4),(5)", 5))
					resetDatabaseExec(t, p, c, "SET stats_fetch_consistency=none", "SET plan_cache_mode="+mode, "CREATE ROLE reset_parameter_reader", "GRANT USAGE ON SCHEMA public TO reset_parameter_reader")
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SHOW plan_cache_mode", []string{"plan_cache_mode"}, []string{"TEXT"}, [][]any{{mode}}))
					targetSQL := "SELECT 'public.reset_parameter_sentinel'::regclass::oid::bigint"
					if name == "pg_stat_reset_single_function_counters" {
						activityExec(t, p, c, "CREATE FUNCTION public.reset_parameter_target() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 7; END$$")
						targetSQL = "SELECT 'public.reset_parameter_target()'::regprocedure::oid::bigint"
					}
					var oid int64
					if err := c.QueryRowContext(p.Ctx, targetSQL).Scan(&oid); err != nil || oid <= 0 {
						t.Fatal("actual reset target identity", oid, err)
					}
					identity := "pg_catalog." + name + "(oid)"
					defer func() {
						c.ExecContext(context.Background(), "RESET ROLE")
						c.ExecContext(context.Background(), "REVOKE EXECUTE ON FUNCTION "+identity+" FROM reset_parameter_reader")
					}()
					var database string
					if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
						t.Fatal(err)
					}
					resetDatabaseValue(t, p, b, c, "SELECT pg_stat_reset_single_function_counters(0::oid)", "pg_stat_reset_single_function_counters", "")
					stamp := resetDatabaseTimestamp(t, p, c, database)
					baseline := statsReportingVector{5, 0, 0, 5, 0, 5, 5}
					statsReportingWant(t, p, b, c, "reset_parameter_sentinel", baseline)
					query := "SELECT pg_catalog." + name + "($1::oid)"
					stmt, err := c.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
					unchanged := func() {
						t.Helper()
						if current := resetDatabaseTimestamp(t, p, c, database); !resetDatabaseSameTime(current, stamp) {
							t.Fatal("noninvoking parameter changed reset timestamp", current, stamp)
						}
						statsReportingWant(t, p, b, c, "reset_parameter_sentinel", baseline)
					}
					deny := func(argument any) {
						t.Helper()
						step := fixture.ErrorDiagnostic(query, activityDiagnostic("42501", "permission denied for function "+name, 0), argument)
						step.SelectRows, step.ErrorStage = true, "execute"
						fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
						unchanged()
					}
					// Keep this exact prepared statement across denied, granted and
					// revoked executions, with both NULL and demanded non-NULL OIDs.
					for _, granted := range []bool{false, true, false} {
						activityExec(t, p, c, "RESET ROLE")
						action := "REVOKE EXECUTE ON FUNCTION " + identity + " FROM reset_parameter_reader"
						if granted {
							action = "GRANT EXECUTE ON FUNCTION " + identity + " TO reset_parameter_reader"
						}
						activityExec(t, p, c, action)
						activityExec(t, p, c, "SET ROLE reset_parameter_reader")
						if granted || mode == "force_custom_plan" {
							resetDatabaseValue(t, p, b, held, query, name, nil, nil)
							unchanged()
						} else {
							deny(nil)
						}
						if !granted {
							deny(oid)
							continue
						}
						resetDatabaseValue(t, p, b, held, query, name, "", oid)
						current := resetDatabaseTimestamp(t, p, c, database)
						if !current.Valid || current.Time.Before(stamp.Time) {
							t.Fatal("granted non-NULL parameter did not stamp database", current, stamp)
						}
						stamp = current
						if name == "pg_stat_reset_single_table_counters" {
							statsReportingWant(t, p, b, c, "reset_parameter_sentinel", statsReportingVector{})
							activityExec(t, p, c, "RESET ROLE")
							statsPublish(t, p, b, "reset_parameter_sentinel", 5, fixture.Exec("INSERT INTO reset_parameter_sentinel VALUES(6),(7),(8),(9),(10)", 5))
							activityExec(t, p, c, "SET ROLE reset_parameter_reader")
						}
						// The revocation phase starts with genuine positive state again.
						statsReportingWant(t, p, b, c, "reset_parameter_sentinel", baseline)
					}
					activityExec(t, p, c, "RESET ROLE")
				})
			})
		}
	}
}
