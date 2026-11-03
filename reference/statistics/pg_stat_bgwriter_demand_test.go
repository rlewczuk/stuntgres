package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatBGWriterProjectionDemand(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"bgwriter_demand"})
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		restoreACLs := bgwriterRestoreACLs(t, p, b, c)
		fixture.StatisticsExec(t, p, b, c, "CREATE ROLE bgwriter_demand", 0)
		defer func() {
			defer restoreACLs()
			ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
			defer cancel()
			for _, query := range []string{"ROLLBACK", "RESET ROLE", "GRANT EXECUTE ON FUNCTION pg_catalog.pg_stat_get_bgwriter_buf_written_clean() TO PUBLIC"} {
				if _, err := c.ExecContext(ctx, query); err != nil {
					t.Error(query, err)
				}
			}
		}()
		fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=snapshot", 0)
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct {
				query, column, typ string
				value any
				capture bool
			}{
				{"SELECT count(*) FROM pg_stat_bgwriter", "count", "INT8", int64(1), false},
				{"SELECT 1 FROM pg_stat_bgwriter", "?column?", "INT4", int64(1), false},
				{"SELECT 1 FROM pg_stat_bgwriter WHERE buffers_clean>=0", "?column?", "INT4", int64(1), true},
				{"SELECT 1 FROM pg_stat_bgwriter WHERE TRUE AND buffers_clean>=0", "?column?", "INT4", int64(1), true},
				{"SELECT 1 FROM pg_stat_bgwriter ORDER BY buffers_alloc", "?column?", "INT4", int64(1), true},
				{"SELECT stats_reset IS NOT NULL AS present FROM pg_stat_bgwriter", "present", "BOOL", true, true},
			} {
				fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
				selected := bgwriterHeld(t, p, c, tc.query)
				if at := bgwriterSnapshotTime(t, p, c); at.Valid {
					t.Fatal("prepare initialized statistics", tc.query, at)
				}
				executor := fixture.Executor(c)
				if prepared {
					executor = selected
				}
				fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Select(tc.query, []string{tc.column}, []string{tc.typ}, [][]any{{tc.value}}))
				if at := bgwriterSnapshotTime(t, p, c); at.Valid != tc.capture {
					t.Fatal("demand-specific snapshot", tc.query, at, tc.capture)
				}
				selected.Statement.Close()
				fixture.StatisticsExec(t, p, b, c, "ROLLBACK", 0)
			}
		}
		for _, query := range []string{"SELECT buffers_clean FROM pg_stat_bgwriter WHERE FALSE", "SELECT buffers_clean FROM pg_stat_bgwriter WHERE FALSE AND buffers_alloc>=0"} {
			selected := bgwriterHeld(t, p, c, query)
			for _, executor := range []fixture.Executor{c, selected} {
				fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
				fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Select(query, []string{"buffers_clean"}, []string{"INT8"}, nil))
				if at := bgwriterSnapshotTime(t, p, c); at.Valid {
					t.Fatal("false target expression fetched cumulative statistics", query, at)
				}
				fixture.StatisticsExec(t, p, b, c, "ROLLBACK", 0)
			}
			selected.Statement.Close()
		}
		fixture.StatisticsExec(t, p, b, c, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_stat_get_bgwriter_buf_written_clean() FROM PUBLIC", 0)
		fixture.StatisticsExec(t, p, b, c, "SET ROLE bgwriter_demand", 0)
		for _, query := range []string{"SELECT count(*) FROM pg_stat_bgwriter", "SELECT 1::bigint AS count FROM pg_stat_bgwriter"} {
			for _, prepared := range []bool{false, true} {
				fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
				step := fixture.Select(query, []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}})
				step.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				if at := bgwriterSnapshotTime(t, p, c); at.Valid {
					t.Fatal("unused revoked getter was fetched", query, at)
				}
				fixture.StatisticsExec(t, p, b, c, "ROLLBACK", 0)
			}
		}
		for _, query := range []string{"SELECT buffers_clean FROM pg_stat_bgwriter", "SELECT buffers_clean FROM pg_stat_bgwriter WHERE FALSE", "SELECT 1 FROM pg_stat_bgwriter WHERE buffers_clean>=0", "SELECT 1 FROM pg_stat_bgwriter ORDER BY buffers_clean"} {
			selected := bgwriterHeld(t, p, c, query)
			for _, executor := range []fixture.Executor{c, selected} {
				fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
				fixture.StatisticsExec(t, p, b, c, "SAVEPOINT denied_demand", 0)
				bgwriterQueryDenied(t, p, b, executor, query, "permission denied for function pg_stat_get_bgwriter_buf_written_clean")
				fixture.StatisticsExec(t, p, b, c, "ROLLBACK TO SAVEPOINT denied_demand", 0)
				if at := bgwriterSnapshotTime(t, p, c); at.Valid {
					t.Fatal("denied getter initialized statistics", query, at)
				}
				fixture.StatisticsExec(t, p, b, c, "ROLLBACK", 0)
			}
			selected.Statement.Close()
		}
		fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
		for _, prepared := range []bool{false, true} {
			step := fixture.Select("SELECT stats_reset FROM pg_stat_bgwriter WHERE FALSE", []string{"stats_reset"}, []string{"TIMESTAMPTZ"}, nil)
			step.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			if at := bgwriterSnapshotTime(t, p, c); at.Valid {
				t.Fatal("unrelated revoked getter or false reset initialized statistics", at)
			}
		}
		fixture.StatisticsExec(t, p, b, c, "ROLLBACK", 0)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT stats_reset IS NOT NULL AS present FROM pg_stat_bgwriter", []string{"present"}, []string{"BOOL"}, [][]any{{true}}))
	})
}
