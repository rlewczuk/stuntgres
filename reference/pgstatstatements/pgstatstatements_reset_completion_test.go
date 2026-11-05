package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsResetCompletion(t *testing.T) {
	for _, planning := range []string{"off", "on"} {
		t.Run(planning, func(t *testing.T) {
			pgssCase(t, true, func(s *pgssScenario) {
				pgssCounterSetup(s)
				pgssResetSeed(s)
				s.work(fixture.Exec("SET pg_stat_statements.track_planning="+planning, 0))
				stmt, err := s.worker.PrepareContext(s.p.Ctx, pgssResetCall)
				if err != nil {
					s.t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: s.worker, Statement: stmt, SQL: pgssResetCall}
				for _, x := range []fixture.Executor{s.worker, held, held} {
					// Each successful reset removes the preceding sample, then
					// its own real terminal contributes precisely one call/row.
					pgssResetScalar(s, x, pgssResetCall, false)
					s.both(fixture.Select("SELECT count(*) AS groups,COALESCE(sum(calls),0)::bigint AS calls,COALESCE(sum(rows),0)::bigint AS rows,COALESCE(sum(plans),0)::bigint AS plans FROM pgss_part.pg_stat_statements", []string{"groups", "calls", "rows", "plans"}, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{int64(1), int64(1), int64(1), int64(0)}}))
					s.entry(pgssResetCall, 1, 1)
					pgssCounterCheck(s, pgssResetCall, 1, 1, pgssCounterWant{serial: true})
				}
				stmt.Close()
				s.entry(pgssResetCall, 1, 1)
				s.work(fixture.Exec("SET pg_stat_statements.track_planning=off", 0))
				minmax := "SELECT pgss_part.pg_stat_statements_reset(minmax_only=>true)"
				for range 2 {
					pgssResetScalar(s, s.worker, minmax, false)
				}
				s.both(fixture.Select("SELECT calls,rows,min_exec_time>0 AND max_exec_time=min_exec_time AS post_reset_sample,min_plan_time=0 AND max_plan_time=0 AS no_plan FROM pgss_part.pg_stat_statements WHERE query LIKE 'SELECT pgss_part.pg_stat_statements_reset(minmax_only=>%'", []string{"calls", "rows", "post_reset_sample", "no_plan"}, []string{"INT8", "INT8", "BOOL", "BOOL"}, [][]any{{int64(2), int64(2), true, true}}))
				// Reset mutates shared statistics, not transactional table state.
				s.work(fixture.Exec("BEGIN", 0))
				defer s.worker.ExecContext(s.p.Ctx, "ROLLBACK")
				pgssResetScalar(s, s.worker, pgssResetCall, false)
				s.work(fixture.Exec("ROLLBACK", 0))
				s.entry(pgssResetCall, 1, 1)
				s.both(fixture.Select("SELECT count(*) AS n FROM pgss_part.pg_stat_statements", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
			})
		})
	}
}
