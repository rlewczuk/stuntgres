package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsResetMinmax(t *testing.T) {
	pgssCase(t, true, func(s *pgssScenario) {
		pgssResetScalar(s, s.observer, pgssResetCall, false)
		s.work(fixture.Exec("SET pg_stat_statements.track_planning=on", 0))
		a, _ := pgssResetSeed(s)
		u, d, qid := pgssResetIdentity(s, a)
		before, info := pgssResetSnapshot(s), pgssResetInfo(s)
		if len(before) != 2 {
			s.t.Fatalf("seed groups=%d, want two independently measured shapes", len(before))
		}
		q := "SELECT pgss_part.pg_stat_statements_reset($1::oid,$2::oid,$3::bigint,true)"
		at := pgssResetScalar(s, s.observer, q, false, u, d, qid)
		pgssResetAssertMinmax(s, before, pgssResetSnapshot(s), func(row []any) bool { return row[3] == qid }, at)
		pgssResetEqual(s.t, pgssResetInfo(s), info)
		before = pgssResetSnapshot(s)
		q = "SELECT pgss_part.pg_stat_statements_reset(minmax_only=>true)"
		at = pgssResetScalar(s, pgssResetHeld(s, q), q, false)
		pgssResetAssertMinmax(s, before, pgssResetSnapshot(s), func([]any) bool { return true }, at)
		pgssResetEqual(s.t, pgssResetInfo(s), info)
		// A new measured execution restores execution extrema only. No fake
		// duration constants: genuine elapsed values are independently positive.
		s.work(fixture.Exec("SET pg_stat_statements.track_planning=off", 0))
		s.work(fixture.Select(a, []string{"pgss_reset_a"}, []string{"INT4"}, [][]any{{int64(1)}}, int64(1)))
		s.both(fixture.Select("SELECT calls,rows,min_plan_time=0 AND max_plan_time=0 AS no_plan_extrema,min_exec_time>0 AND max_exec_time=min_exec_time AS new_exec_extrema,minmax_stats_since=$2 AS same_reset FROM pgss_part.pg_stat_statements WHERE queryid=$1", []string{"calls", "rows", "no_plan_extrema", "new_exec_extrema", "same_reset"}, []string{"INT8", "INT8", "BOOL", "BOOL", "BOOL"}, [][]any{{int64(3), int64(4), true, true, true}}, qid, at))
		pgssResetEqual(s.t, pgssResetInfo(s), info)
	})
}
