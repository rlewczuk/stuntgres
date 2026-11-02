package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsUtilityIdentity(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pgss_utility_identity_owner"})
	pgssCase(t, true, func(s *pgssScenario) {
		s.work(fixture.Exec("SET pg_stat_statements.track_utility=on", 0))
		// A genuine fresh effective-user key controls first source spelling
		// without resetting cluster history left by other utility scenarios.
		s.exec("CREATE ROLE pgss_utility_identity_owner")
		s.exec("GRANT USAGE ON SCHEMA pgss_part TO pgss_utility_identity_owner")
		s.work(fixture.Exec("SET ROLE pgss_utility_identity_owner", 0))
		s.exec("SET ROLE pgss_utility_identity_owner")
		defer s.worker.ExecContext(s.p.Ctx, "RESET ROLE")
		defer s.observer.ExecContext(s.p.Ctx, "RESET ROLE")
		baseline := func(query string) int64 {
			s.t.Helper()
			var calls, rows int64
			err := s.observer.QueryRowContext(s.p.Ctx, "SELECT COALESCE(sum(calls),0)::bigint,COALESCE(sum(rows),0)::bigint FROM pgss_part.pg_stat_statements WHERE query=$1 AND userid=(SELECT oid FROM pg_roles WHERE rolname=current_user) AND dbid=(SELECT oid FROM pg_database WHERE datname=current_database())", query).Scan(&calls, &rows)
			if err != nil || rows != 0 {
				s.t.Fatalf("utility baseline %q calls=%d rows=%d error=%v", query, calls, rows, err)
			}
			return calls
		}
		savepointBefore := baseline("SAVEPOINT $1")
		releaseBefore := baseline("RELEASE $1")
		beginBefore := baseline("BEGIN")
		startBefore := baseline("START TRANSACTION")
		s.work(fixture.Exec("BEGIN", 0))
		defer s.worker.ExecContext(s.p.Ctx, "ROLLBACK")
		for i, name := range []string{"pgss_identity_first", "pgss_identity_second"} {
			for _, verb := range []string{"SAVEPOINT ", "RELEASE "} {
				step := fixture.Exec(verb+name, 0)
				if i == 1 {
					step = fixture.Prepared(step)
				}
				s.work(step)
			}
		}
		s.work(fixture.Exec("COMMIT", 0))
		s.entry("SAVEPOINT $1", savepointBefore+2, 0)
		s.entry("RELEASE $1", releaseBefore+2, 0)
		s.entry("BEGIN", beginBefore+1, 0)
		// Released TransactionStmtKind retains BEGIN versus START even when
		// their transactional effects are equivalent.
		s.work(fixture.Prepared(fixture.Exec("START TRANSACTION", 0)))
		s.work(fixture.Exec("COMMIT", 0))
		s.entry("BEGIN", beginBefore+1, 0)
		s.entry("START TRANSACTION", startBefore+1, 0)
		s.both(fixture.Select("SELECT count(DISTINCT queryid) AS kinds FROM pgss_part.pg_stat_statements WHERE query IN ('BEGIN','START TRANSACTION') AND userid=(SELECT oid FROM pg_roles WHERE rolname=current_user) AND dbid=(SELECT oid FROM pg_database WHERE datname=current_database())", []string{"kinds"}, []string{"INT8"}, [][]any{{int64(2)}}))
	})
}
