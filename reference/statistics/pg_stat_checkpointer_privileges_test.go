package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

var checkpointerGetters = []string{"pg_stat_get_checkpointer_num_timed", "pg_stat_get_checkpointer_num_requested", "pg_stat_get_checkpointer_num_performed", "pg_stat_get_checkpointer_restartpoints_timed", "pg_stat_get_checkpointer_restartpoints_requested", "pg_stat_get_checkpointer_restartpoints_performed", "pg_stat_get_checkpointer_write_time", "pg_stat_get_checkpointer_sync_time", "pg_stat_get_checkpointer_buffers_written", "pg_stat_get_checkpointer_slru_written", "pg_stat_get_checkpointer_stat_reset_time"}

func checkpointerQueryDenied(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, query, message string, args ...any) {
	t.Helper()
	step := fixture.ErrorDiagnostic(query, checkpointerDiagnostic("42501", message), args...)
	step.SelectRows, step.ErrorStage = true, "execute"
	fixture.RunStep(t, p.Ctx, e, b.Recorder, step)
}

func TestReferencePGStatCheckpointerPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"checkpointer_reader", "checkpointer_monitor", "checkpointer_stats"})
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		restoreACLs := checkpointerRestoreACLs(t, p, b, c)
		for _, query := range []string{"CREATE ROLE checkpointer_reader", "CREATE ROLE checkpointer_monitor", "CREATE ROLE checkpointer_stats", "GRANT pg_monitor TO checkpointer_monitor", "GRANT pg_read_all_stats TO checkpointer_stats"} {
			fixture.StatisticsExec(t, p, b, c, query, 0)
		}
		defer func() {
			defer restoreACLs()
			ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
			defer cancel()
			queries := []string{"ROLLBACK", "RESET ROLE", "GRANT SELECT ON pg_catalog.pg_stat_checkpointer TO PUBLIC", "REVOKE SELECT (stats_reset) ON pg_catalog.pg_stat_checkpointer FROM checkpointer_reader", "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_stat_reset_shared(text) FROM checkpointer_reader"}
			for _, getter := range checkpointerGetters {
				queries = append(queries, "GRANT EXECUTE ON FUNCTION pg_catalog."+getter+"() TO PUBLIC")
			}
			for _, query := range queries {
				if _, err := c.ExecContext(ctx, query); err != nil {
					t.Error("ACL cleanup", query, err)
				}
			}
		}()
		before := checkpointerGet(t, p, b, c)
		held := checkpointerHeld(t, p, c, checkpointerSQL)
		defer held.Statement.Close()
		const resetSQL = "SELECT pg_catalog.pg_stat_reset_shared($1::text)"
		resetHeld := checkpointerHeld(t, p, c, resetSQL)
		defer resetHeld.Statement.Close()
		for _, role := range []string{"checkpointer_reader", "checkpointer_monitor", "checkpointer_stats"} {
			fixture.StatisticsExec(t, p, b, c, "SET ROLE "+role, 0)
			for _, executor := range []fixture.Executor{c, held} {
				checkpointerSameReset(t, before, checkpointerGet(t, p, b, executor))
			}
			for _, executor := range []fixture.Executor{c, resetHeld} {
				checkpointerQueryDenied(t, p, b, executor, resetSQL, "permission denied for function pg_stat_reset_shared", "checkpointer")
			}
			fixture.StatisticsExec(t, p, b, c, "RESET ROLE", 0)
		}
		fixture.StatisticsExec(t, p, b, c, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_stat_reset_shared(text) TO checkpointer_reader", 0)
		lower := checkpointerClock(t, p, c)
		fixture.StatisticsExec(t, p, b, c, "SET ROLE checkpointer_reader", 0)
		checkpointerVoid(t, p, b, resetHeld, resetSQL, "pg_stat_reset_shared", "checkpointer")
		after := checkpointerGet(t, p, b, held)
		checkpointerEvent(t, before, after, lower, checkpointerClock(t, p, c))
		fixture.StatisticsExec(t, p, b, c, "RESET ROLE", 0)
		fixture.StatisticsExec(t, p, b, c, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_stat_reset_shared(text) FROM checkpointer_reader", 0)

		fixture.StatisticsExec(t, p, b, c, "REVOKE SELECT ON pg_catalog.pg_stat_checkpointer FROM PUBLIC", 0)
		fixture.StatisticsExec(t, p, b, c, "SET ROLE checkpointer_reader", 0)
		for _, executor := range []fixture.Executor{c, held} {
			checkpointerQueryDenied(t, p, b, executor, checkpointerSQL, "permission denied for view pg_stat_checkpointer")
		}
		fixture.StatisticsExec(t, p, b, c, "RESET ROLE", 0)
		fixture.StatisticsExec(t, p, b, c, "GRANT SELECT (stats_reset) ON pg_catalog.pg_stat_checkpointer TO checkpointer_reader", 0)
		fixture.StatisticsExec(t, p, b, c, "SET ROLE checkpointer_reader", 0)
		const columnSQL = "SELECT stats_reset FROM pg_catalog.pg_stat_checkpointer"
		for _, step := range []fixture.Step{
			fixture.Select(columnSQL, []string{"stats_reset"}, []string{"TIMESTAMPTZ"}, [][]any{{fixture.Normalize(after.reset)}}),
			fixture.Prepared(fixture.Select(columnSQL, []string{"stats_reset"}, []string{"TIMESTAMPTZ"}, [][]any{{fixture.Normalize(after.reset)}})),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
		}
		for _, query := range []string{checkpointerSQL, columnSQL + " WHERE num_timed=0", columnSQL + " ORDER BY num_done"} {
			checkpointerQueryDenied(t, p, b, c, query, "permission denied for view pg_stat_checkpointer")
		}
		fixture.StatisticsExec(t, p, b, c, "RESET ROLE", 0)
		fixture.StatisticsExec(t, p, b, c, "GRANT SELECT ON pg_catalog.pg_stat_checkpointer TO PUBLIC", 0)

		for i, getter := range checkpointerGetters {
			query := "SELECT " + checkpointerNames[i] + " IS NOT NULL AS present FROM pg_catalog.pg_stat_checkpointer"
			selected := checkpointerHeld(t, p, c, query)
			defer selected.Statement.Close()
			fixture.StatisticsExec(t, p, b, c, "REVOKE EXECUTE ON FUNCTION pg_catalog."+getter+"() FROM PUBLIC", 0)
			fixture.StatisticsExec(t, p, b, c, "SET ROLE checkpointer_reader", 0)
			for _, executor := range []fixture.Executor{c, selected} {
				checkpointerQueryDenied(t, p, b, executor, query, "permission denied for function "+getter)
			}
			checkpointerQueryDenied(t, p, b, held, checkpointerSQL, "permission denied for function "+getter)
			for _, dependent := range []string{
				"SELECT 1 FROM pg_catalog.pg_stat_checkpointer WHERE " + checkpointerNames[i] + " IS NOT NULL",
				"SELECT 1 FROM pg_catalog.pg_stat_checkpointer ORDER BY " + checkpointerNames[i],
				query + " WHERE FALSE",
			} {
				selectedDependency := checkpointerHeld(t, p, c, dependent)
				for _, executor := range []fixture.Executor{c, selectedDependency} {
					checkpointerQueryDenied(t, p, b, executor, dependent, "permission denied for function "+getter)
				}
				selectedDependency.Statement.Close()
			}
			other := (i + 1) % len(checkpointerNames)
			unrelated := fixture.Select("SELECT "+checkpointerNames[other]+" IS NOT NULL AS present FROM pg_catalog.pg_stat_checkpointer", []string{"present"}, []string{"BOOL"}, [][]any{{true}})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, unrelated)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(unrelated))
			fixture.StatisticsExec(t, p, b, c, "RESET ROLE", 0)
			fixture.StatisticsExec(t, p, b, c, "GRANT EXECUTE ON FUNCTION pg_catalog."+getter+"() TO PUBLIC", 0)
			fixture.StatisticsExec(t, p, b, c, "SET ROLE checkpointer_reader", 0)
			for _, executor := range []fixture.Executor{c, selected} {
				fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Select(query, []string{"present"}, []string{"BOOL"}, [][]any{{true}}))
			}
			fixture.StatisticsExec(t, p, b, c, "RESET ROLE", 0)
		}
	})
}
