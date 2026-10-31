package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStatisticsAlterTransactions(t *testing.T) {
	withStatisticsAlter(t, func(t *testing.T, s ndistinctScenario) {
		statisticsAlterSetup(s)
		var owner string
		if err := s.conn.QueryRowContext(s.pair.Ctx, "SELECT current_user").Scan(&owner); err != nil {
			t.Fatal(err)
		}
		oid := fixture.DeparseDefinitionID(t, s.conn, "SELECT oid FROM pg_statistic_ext WHERE stxname='sa_before'")
		backend := s.pair.Backends()[0]
		if s.pg {
			backend = s.pair.Backends()[1]
		}
		fresh := func(step fixture.Step) { fixture.RunStep(t, s.pair.Ctx, backend.DB, backend.Recorder, step) }
		for _, tc := range []struct {
			sql, name, schema, owner string
			target                   any
		}{
			{"ALTER STATISTICS sa_before RENAME TO sa_after", "sa_after", "public", owner, nil},
			{"ALTER STATISTICS sa_before SET SCHEMA sa_destination", "sa_before", "sa_destination", owner, nil},
			{"ALTER STATISTICS sa_before OWNER TO sa_recipient", "sa_before", "public", "sa_recipient", nil},
			{"ALTER STATISTICS sa_before SET STATISTICS 0", "sa_before", "public", owner, int64(0)},
		} {
			s.run(fixture.Exec("BEGIN", 0))
			s.run(fixture.Exec(tc.sql, 0))
			s.run(statisticsAlterMetadata(tc.name, tc.schema, tc.owner, tc.target))
			fresh(statisticsAlterMetadata("sa_before", "public", owner, nil))
			statisticsAlterFacts(t, s, tc.name)
			s.run(fixture.Exec("ROLLBACK", 0))
			s.run(statisticsAlterMetadata("sa_before", "public", owner, nil))
			if got := fixture.DeparseDefinitionID(t, s.conn, "SELECT oid FROM pg_statistic_ext WHERE stxname='sa_before'"); got != oid {
				t.Fatal("rollback replaced source")
			}
		}
		s.run(fixture.Exec("BEGIN", 0))
		s.run(fixture.Exec("SAVEPOINT metadata", 0))
		composed := []string{"ALTER STATISTICS sa_before RENAME TO sa_after", "ALTER STATISTICS sa_after SET SCHEMA sa_destination", "ALTER STATISTICS sa_destination.sa_after OWNER TO sa_recipient", "ALTER STATISTICS sa_destination.sa_after SET STATISTICS 10000"}
		for _, q := range composed {
			s.run(fixture.Exec(q, 0))
		}
		s.run(statisticsAlterMetadata("sa_after", "sa_destination", "sa_recipient", int64(10000)))
		s.run(fixture.Exec("ROLLBACK TO SAVEPOINT metadata", 0))
		s.run(statisticsAlterMetadata("sa_before", "public", owner, nil))
		statisticsAlterFacts(t, s, "sa_before")
		for _, q := range composed {
			s.run(fixture.Exec(q, 0))
		}
		const failedQuery = "ALTER STATISTICS sa_destination.sa_after SET STATISTICS 0"
		heldFailure, err := s.conn.PrepareContext(s.pair.Ctx, failedQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer heldFailure.Close()
		s.run(statisticsError("ALTER STATISTICS sa_destination.sa_after RENAME TO sa_after", "42710", `statistics object "sa_after" already exists in schema "sa_destination"`))
		aborted := statisticsError(failedQuery, "25P02", "current transaction is aborted, commands ignored until end of transaction block")
		// A fresh Prepare is rejected by the already-aborted transaction; the
		// previously prepared statement independently reaches Execute rejection.
		if s.prepared {
			aborted.ErrorStage = "prepare"
		}
		s.run(aborted)
		aborted.ErrorStage = "execute"
		fixture.RunStep(t, s.pair.Ctx, fixture.HeldStatement{s.conn, heldFailure, failedQuery}, backend.Recorder, aborted)
		s.run(fixture.Exec("ROLLBACK TO SAVEPOINT metadata", 0))
		s.run(statisticsAlterMetadata("sa_before", "public", owner, nil))
		for _, q := range composed {
			s.run(fixture.Exec(q, 0))
		}
		s.run(fixture.Exec("COMMIT", 0))
		fresh(statisticsAlterMetadata("sa_after", "sa_destination", "sa_recipient", int64(10000)))
		statisticsAlterFacts(t, s, "sa_after")
		if got := fixture.DeparseDefinitionID(t, s.conn, "SELECT oid FROM pg_statistic_ext WHERE stxname='sa_after'"); got != oid {
			t.Fatal("commit replaced source")
		}
	})
}
