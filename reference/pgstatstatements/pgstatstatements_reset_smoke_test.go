package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsResetFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pgss_reset_user", "pgss_reset_group", "pgss_reset_control_owner", "pgss_reset_control_user"})
	fixture.SchemaReferenceCleanup(t, "pgss_reset_moved")
	var databases []string
	fixture.DatabaseReferenceCleanup(t, &databases)
	pgssCase(t, true, func(s *pgssScenario) {
		pgssResetScalar(s, s.observer, pgssResetCall, false)
		a, b := pgssResetSeed(s)
		s.entry(a, 2, 3)
		s.entry(b, 1, 1)
		u, d, qid := pgssResetIdentity(s, a)
		_, _, otherID := pgssResetIdentity(s, b)
		before, info := pgssResetSnapshot(s), pgssResetInfo(s)
		q := "SELECT pgss_part.pg_stat_statements_reset($1::oid,$2::oid,$3::bigint)"
		pgssResetScalar(s, s.observer, q, false, u, d, qid)
		s.counts(a, 0, 0, 0)
		var surviving [][]any
		for _, row := range before {
			if row[3] != qid {
				surviving = append(surviving, row)
			}
		}
		pgssResetEqual(s.t, pgssResetSnapshot(s), surviving)
		pgssResetEqual(s.t, pgssResetInfo(s), info)
		// Independently reseed before the prepared destructive route.
		s.work(fixture.Select(a, []string{"pgss_reset_a"}, []string{"INT4"}, [][]any{{int64(1)}}, int64(1)))
		pgssResetScalar(s, pgssResetHeld(s, q), q, false, u, d, qid)
		pgssResetEqual(s.t, pgssResetSnapshot(s), surviving)
		before = pgssResetSnapshot(s)
		q = "SELECT pgss_part.pg_stat_statements_reset(0,0,$1::bigint,true)"
		at := pgssResetScalar(s, pgssResetHeld(s, q), q, false, otherID)
		pgssResetAssertMinmax(s, before, pgssResetSnapshot(s), func(row []any) bool { return row[3] == otherID }, at)
		pgssResetEqual(s.t, pgssResetInfo(s), info)
		before = pgssResetSnapshot(s)
		for _, args := range []string{"NULL::oid,0,0,false", "0,NULL::oid,0,false", "0,0,NULL::bigint,false", "0,0,0,NULL::boolean"} {
			q := "SELECT pgss_part.pg_stat_statements_reset(" + args + ")"
			pgssResetScalar(s, s.observer, q, true)
			pgssResetScalar(s, pgssResetHeld(s, q), q, true)
		}
		pgssResetEqual(s.t, pgssResetSnapshot(s), before)
		pgssResetEqual(s.t, pgssResetInfo(s), info)
		pgssResetPrivileges(s, false, &databases)
		// Actual enabled execution, not a track=none reset-only smoke.
		pgssResetScalar(s, s.worker, pgssResetCall, false)
		s.entry(pgssResetCall, 1, 1)
		s.both(fixture.Select("SELECT count(*) AS groups,COALESCE(sum(calls),0)::bigint AS calls,COALESCE(sum(rows),0)::bigint AS rows FROM pgss_part.pg_stat_statements", []string{"groups", "calls", "rows"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(1), int64(1), int64(1)}}))
		pgssResetStoredControls(s)
	})
}
