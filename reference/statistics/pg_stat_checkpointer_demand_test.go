package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func checkpointerProjectionDemand(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"checkpointer_demand"})
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		restoreACLs := checkpointerRestoreACLs(t, p, b, c)
		fixture.StatisticsExec(t, p, b, c, "CREATE ROLE checkpointer_demand", 0)
		defer func() {
			defer restoreACLs()
			ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
			defer cancel()
			for _, query := range []string{"ROLLBACK", "RESET ROLE", "GRANT EXECUTE ON FUNCTION pg_catalog.pg_stat_get_checkpointer_num_timed() TO PUBLIC"} {
				if _, err := c.ExecContext(ctx, query); err != nil {
					t.Error(query, err)
				}
			}
		}()
		fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=snapshot", 0)
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct {
				query, column, typ string
				value              any
				capture            bool
			}{
				{"SELECT count(*) FROM pg_stat_checkpointer", "count", "INT8", int64(1), false},
				{"SELECT 1 FROM pg_stat_checkpointer", "?column?", "INT4", int64(1), false},
				{"SELECT 1 FROM pg_stat_checkpointer WHERE num_timed>=0", "?column?", "INT4", int64(1), true},
				{"SELECT 1 FROM pg_stat_checkpointer WHERE TRUE AND num_timed>=0", "?column?", "INT4", int64(1), true},
				{"SELECT 1 FROM pg_stat_checkpointer ORDER BY num_done", "?column?", "INT4", int64(1), true},
				{"SELECT stats_reset IS NOT NULL AS present FROM pg_stat_checkpointer", "present", "BOOL", true, true},
			} {
				fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
				selected := checkpointerHeld(t, p, c, tc.query)
				if at := checkpointerSnapshotTime(t, p, c); at.Valid {
					t.Fatal("prepare initialized statistics", tc.query, at)
				}
				executor := fixture.Executor(c)
				if prepared {
					executor = selected
				}
				fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Select(tc.query, []string{tc.column}, []string{tc.typ}, [][]any{{tc.value}}))
				if at := checkpointerSnapshotTime(t, p, c); at.Valid != tc.capture {
					t.Fatal("demand-specific snapshot", tc.query, at, tc.capture)
				}
				selected.Statement.Close()
				fixture.StatisticsExec(t, p, b, c, "ROLLBACK", 0)
			}
		}
		for _, query := range []string{"SELECT num_timed FROM pg_stat_checkpointer WHERE FALSE", "SELECT num_timed FROM pg_stat_checkpointer WHERE FALSE AND num_done>=0"} {
			selected := checkpointerHeld(t, p, c, query)
			for _, executor := range []fixture.Executor{c, selected} {
				fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
				fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Select(query, []string{"num_timed"}, []string{"INT8"}, nil))
				if at := checkpointerSnapshotTime(t, p, c); at.Valid {
					t.Fatal("false target expression fetched cumulative statistics", query, at)
				}
				fixture.StatisticsExec(t, p, b, c, "ROLLBACK", 0)
			}
			selected.Statement.Close()
		}
		fixture.StatisticsExec(t, p, b, c, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_stat_get_checkpointer_num_timed() FROM PUBLIC", 0)
		fixture.StatisticsExec(t, p, b, c, "SET ROLE checkpointer_demand", 0)
		for _, query := range []string{"SELECT count(*) FROM pg_stat_checkpointer", "SELECT 1::bigint AS count FROM pg_stat_checkpointer"} {
			for _, prepared := range []bool{false, true} {
				fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
				step := fixture.Select(query, []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}})
				step.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				if at := checkpointerSnapshotTime(t, p, c); at.Valid {
					t.Fatal("unused revoked getter was fetched", query, at)
				}
				fixture.StatisticsExec(t, p, b, c, "ROLLBACK", 0)
			}
		}
		for _, query := range []string{"SELECT num_timed FROM pg_stat_checkpointer", "SELECT num_timed FROM pg_stat_checkpointer WHERE FALSE", "SELECT 1 FROM pg_stat_checkpointer WHERE num_timed>=0", "SELECT 1 FROM pg_stat_checkpointer ORDER BY num_timed"} {
			selected := checkpointerHeld(t, p, c, query)
			for _, executor := range []fixture.Executor{c, selected} {
				fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
				fixture.StatisticsExec(t, p, b, c, "SAVEPOINT denied_demand", 0)
				checkpointerQueryDenied(t, p, b, executor, query, "permission denied for function pg_stat_get_checkpointer_num_timed")
				fixture.StatisticsExec(t, p, b, c, "ROLLBACK TO SAVEPOINT denied_demand", 0)
				if at := checkpointerSnapshotTime(t, p, c); at.Valid {
					t.Fatal("denied getter initialized statistics", query, at)
				}
				fixture.StatisticsExec(t, p, b, c, "ROLLBACK", 0)
			}
			selected.Statement.Close()
		}
		fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
		for _, prepared := range []bool{false, true} {
			step := fixture.Select("SELECT stats_reset FROM pg_stat_checkpointer WHERE FALSE", []string{"stats_reset"}, []string{"TIMESTAMPTZ"}, nil)
			step.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			if at := checkpointerSnapshotTime(t, p, c); at.Valid {
				t.Fatal("unrelated revoked getter or false reset initialized statistics", at)
			}
		}
		fixture.StatisticsExec(t, p, b, c, "ROLLBACK", 0)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT stats_reset IS NOT NULL AS present FROM pg_stat_checkpointer", []string{"present"}, []string{"BOOL"}, [][]any{{true}}))
	})
}

func TestReferencePGStatCheckpointerDemand(t *testing.T) {
	t.Run("projection", checkpointerProjectionDemand)
	t.Run("aggregate", checkpointerAggregateDemand)
	t.Run("occurrence suppression", checkpointerOccurrenceSuppression)
}

func checkpointerOccurrenceSuppression(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=snapshot", 0)
		const query = "SELECT num_timed IS NOT NULL AS present FROM pg_stat_checkpointer WHERE FALSE UNION ALL SELECT num_timed IS NOT NULL AS present FROM pg_stat_checkpointer"
		held := checkpointerHeld(t, p, c, query)
		defer held.Statement.Close()
		for _, executor := range []fixture.Executor{c, held} {
			fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
			fixture.StatisticsClearSnapshot(t, p, b, c)
			if at := checkpointerSnapshotTime(t, p, c); at.Valid {
				t.Fatal("metadata-only preparation captured")
			}
			fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Select(query, []string{"present"}, []string{"BOOL"}, [][]any{{true}}))
			if at := checkpointerSnapshotTime(t, p, c); !at.Valid {
				t.Fatal("false occurrence suppressed live sibling access")
			}
			fixture.StatisticsExec(t, p, b, c, "ROLLBACK", 0)
		}
	})
}
