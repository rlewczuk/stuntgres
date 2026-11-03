package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

var bgwriterGetters = []string{"pg_stat_get_bgwriter_buf_written_clean", "pg_stat_get_bgwriter_maxwritten_clean", "pg_stat_get_buf_alloc", "pg_stat_get_bgwriter_stat_reset_time"}

func bgwriterQueryDenied(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, query, message string, args ...any) {
	t.Helper()
	step := fixture.ErrorDiagnostic(query, bgwriterDiagnostic("42501", message), args...)
	step.SelectRows, step.ErrorStage = true, "execute"
	fixture.RunStep(t, p.Ctx, e, b.Recorder, step)
}

func TestReferencePGStatBGWriterPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"bgwriter_reader", "bgwriter_monitor", "bgwriter_stats"})
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		restoreACLs := bgwriterRestoreACLs(t, p, b, c)
		for _, query := range []string{"CREATE ROLE bgwriter_reader", "CREATE ROLE bgwriter_monitor", "CREATE ROLE bgwriter_stats", "GRANT pg_monitor TO bgwriter_monitor", "GRANT pg_read_all_stats TO bgwriter_stats"} {
			fixture.StatisticsExec(t, p, b, c, query, 0)
		}
		defer func() {
			defer restoreACLs()
			ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
			defer cancel()
			queries := []string{"ROLLBACK", "RESET ROLE", "GRANT SELECT ON pg_catalog.pg_stat_bgwriter TO PUBLIC", "REVOKE SELECT (stats_reset) ON pg_catalog.pg_stat_bgwriter FROM bgwriter_reader", "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_stat_reset_shared(text) FROM bgwriter_reader"}
			for _, getter := range bgwriterGetters {
				queries = append(queries, "GRANT EXECUTE ON FUNCTION pg_catalog."+getter+"() TO PUBLIC")
			}
			for _, query := range queries {
				if _, err := c.ExecContext(ctx, query); err != nil {
					t.Error("ACL cleanup", query, err)
				}
			}
		}()
		before := bgwriterGet(t, p, b, c)
		held := bgwriterHeld(t, p, c, bgwriterSQL)
		defer held.Statement.Close()
		const resetSQL = "SELECT pg_catalog.pg_stat_reset_shared($1::text)"
		resetHeld := bgwriterHeld(t, p, c, resetSQL)
		defer resetHeld.Statement.Close()
		for _, role := range []string{"bgwriter_reader", "bgwriter_monitor", "bgwriter_stats"} {
			fixture.StatisticsExec(t, p, b, c, "SET ROLE "+role, 0)
			for _, executor := range []fixture.Executor{c, held} {
				bgwriterSameReset(t, before, bgwriterGet(t, p, b, executor))
			}
			for _, executor := range []fixture.Executor{c, resetHeld} {
				bgwriterQueryDenied(t, p, b, executor, resetSQL, "permission denied for function pg_stat_reset_shared", "bgwriter")
			}
			fixture.StatisticsExec(t, p, b, c, "RESET ROLE", 0)
		}
		fixture.StatisticsExec(t, p, b, c, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_stat_reset_shared(text) TO bgwriter_reader", 0)
		lower := bgwriterClock(t, p, c)
		fixture.StatisticsExec(t, p, b, c, "SET ROLE bgwriter_reader", 0)
		bgwriterVoid(t, p, b, resetHeld, resetSQL, "pg_stat_reset_shared", "bgwriter")
		after := bgwriterGet(t, p, b, held)
		bgwriterEvent(t, before, after, lower, bgwriterClock(t, p, c))
		fixture.StatisticsExec(t, p, b, c, "RESET ROLE", 0)
		fixture.StatisticsExec(t, p, b, c, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_stat_reset_shared(text) FROM bgwriter_reader", 0)

		fixture.StatisticsExec(t, p, b, c, "REVOKE SELECT ON pg_catalog.pg_stat_bgwriter FROM PUBLIC", 0)
		fixture.StatisticsExec(t, p, b, c, "SET ROLE bgwriter_reader", 0)
		for _, executor := range []fixture.Executor{c, held} {
			bgwriterQueryDenied(t, p, b, executor, bgwriterSQL, "permission denied for view pg_stat_bgwriter")
		}
		fixture.StatisticsExec(t, p, b, c, "RESET ROLE", 0)
		fixture.StatisticsExec(t, p, b, c, "GRANT SELECT (stats_reset) ON pg_catalog.pg_stat_bgwriter TO bgwriter_reader", 0)
		fixture.StatisticsExec(t, p, b, c, "SET ROLE bgwriter_reader", 0)
		const columnSQL = "SELECT stats_reset FROM pg_catalog.pg_stat_bgwriter"
		for _, step := range []fixture.Step{
			fixture.Select(columnSQL, []string{"stats_reset"}, []string{"TIMESTAMPTZ"}, [][]any{{fixture.Normalize(after.reset)}}),
			fixture.Prepared(fixture.Select(columnSQL, []string{"stats_reset"}, []string{"TIMESTAMPTZ"}, [][]any{{fixture.Normalize(after.reset)}})),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
		}
		for _, query := range []string{bgwriterSQL, columnSQL+" WHERE buffers_clean=0", columnSQL+" ORDER BY buffers_alloc"} {
			bgwriterQueryDenied(t, p, b, c, query, "permission denied for view pg_stat_bgwriter")
		}
		fixture.StatisticsExec(t, p, b, c, "RESET ROLE", 0)
		fixture.StatisticsExec(t, p, b, c, "GRANT SELECT ON pg_catalog.pg_stat_bgwriter TO PUBLIC", 0)

		for i, getter := range bgwriterGetters {
			query := "SELECT "+bgwriterNames[i]+" IS NOT NULL AS present FROM pg_catalog.pg_stat_bgwriter"
			selected := bgwriterHeld(t, p, c, query)
			defer selected.Statement.Close()
			fixture.StatisticsExec(t, p, b, c, "REVOKE EXECUTE ON FUNCTION pg_catalog."+getter+"() FROM PUBLIC", 0)
			fixture.StatisticsExec(t, p, b, c, "SET ROLE bgwriter_reader", 0)
			for _, executor := range []fixture.Executor{c, selected} {
				bgwriterQueryDenied(t, p, b, executor, query, "permission denied for function "+getter)
			}
			bgwriterQueryDenied(t, p, b, held, bgwriterSQL, "permission denied for function "+getter)
			other := (i+1)%4
			unrelated := fixture.Select("SELECT "+bgwriterNames[other]+" IS NOT NULL AS present FROM pg_catalog.pg_stat_bgwriter", []string{"present"}, []string{"BOOL"}, [][]any{{true}})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, unrelated)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(unrelated))
			fixture.StatisticsExec(t, p, b, c, "RESET ROLE", 0)
			fixture.StatisticsExec(t, p, b, c, "GRANT EXECUTE ON FUNCTION pg_catalog."+getter+"() TO PUBLIC", 0)
			fixture.StatisticsExec(t, p, b, c, "SET ROLE bgwriter_reader", 0)
			for _, executor := range []fixture.Executor{c, selected} {
				fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Select(query, []string{"present"}, []string{"BOOL"}, [][]any{{true}}))
			}
			fixture.StatisticsExec(t, p, b, c, "RESET ROLE", 0)
		}
	})
}
