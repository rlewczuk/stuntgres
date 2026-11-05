package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func pgssCounterReadFrontiers(t *testing.T) {
	pgssCase(t, true, func(s *pgssScenario) {
		pgssCounterSetup(s)
		s.exec("CREATE TABLE pgss_part.pgss_counter_frontier_outer(k integer)")
		s.exec("CREATE TABLE pgss_part.pgss_counter_frontier_inner(k integer)")
		s.run(fixture.Exec("INSERT INTO pgss_part.pgss_counter_frontier_outer VALUES(1),(2)", 2))
		s.run(fixture.Exec("INSERT INTO pgss_part.pgss_counter_frontier_inner VALUES(1)", 1))
		q := "SELECT a.k,b.k FROM pgss_part.pgss_counter_frontier_outer a CROSS JOIN pgss_part.pgss_counter_frontier_inner b ORDER BY a.k"
		s.work(fixture.Prepared(fixture.Select(q, []string{"k", "k"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(1)}})))
		// Rewinding one reached inner stage does not reopen its read frontier.
		pgssCounterCheck(s, q, 1, 2, pgssCounterWant{anySharedSplit: true, sharedRequests: 2, pgShared: true, serial: true})
		q = "SELECT d.k,(SELECT max(x.k) FROM pgss_part.pgss_counter_frontier_inner x WHERE x.k<=d.k) AS v FROM pgss_part.pgss_counter_frontier_outer d ORDER BY d.k"
		s.work(fixture.Select(q, []string{"k", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(1)}}))
		// Two genuinely correlated invocations, plus the outer physical source.
		pgssCounterCheck(s, q, 1, 2, pgssCounterWant{anySharedSplit: true, sharedRequests: 3, pgShared: true, serial: true})
		q = "SELECT x FROM generate_series($1::integer,$2::integer) g(x) ORDER BY x"
		s.work(fixture.Prepared(fixture.Select(q, []string{"x"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}, int64(1), int64(2))))
		pgssCounterCheck(s, q, 1, 2, pgssCounterWant{serial: true}) // No native storage lease.
		s.exec("CREATE EXTENSION tsm_system_rows SCHEMA pgss_part")
		defer s.observer.ExecContext(s.p.Ctx, "DROP EXTENSION IF EXISTS tsm_system_rows")
		q = "SELECT k FROM pgss_part.pgss_counter_frontier_outer sampled TABLESAMPLE pgss_part.system_rows($1::bigint)"
		s.counts(q, 0, 0, 0)
		s.work(fixture.Prepared(fixture.Select(q, []string{"k"}, []string{"INT4"}, nil, int64(0))))
		pgssCounterCheck(s, q, 1, 0, pgssCounterWant{serial: true})
		before := pgssCounterSnapshot(s, q)
		failure := fixture.ImplicitError(q, fixture.Diagnostic{Code: "2202H", Message: "sample size must not be negative"}, int64(-1))
		failure.SelectRows, failure.ErrorStage = true, "execute"
		s.work(fixture.Prepared(failure))
		pgssResetEqual(s.t, pgssCounterSnapshot(s, q), before)
		s.counts(q, 1, 1, 0)
	})
}
