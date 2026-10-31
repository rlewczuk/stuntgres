package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStatisticsAlterOwnership(t *testing.T) {
	withStatisticsAlter(t, func(t *testing.T, s ndistinctScenario) {
		statisticsAlterSetup(s)
		var sessionOwner string
		if err := s.conn.QueryRowContext(s.pair.Ctx, "SELECT session_user").Scan(&sessionOwner); err != nil {
			t.Fatal(err)
		}
		for _, q := range []string{"CREATE ROLE sa_actor", "CREATE ROLE sa_owner", "GRANT USAGE ON SCHEMA public TO sa_actor,sa_owner,sa_recipient", "ALTER STATISTICS sa_before OWNER TO sa_actor", "GRANT sa_recipient TO sa_actor WITH INHERIT TRUE, SET FALSE", "SET ROLE sa_actor"} {
			s.run(fixture.Exec(q, 0))
		}
		defer s.conn.ExecContext(s.pair.Ctx, "RESET ROLE")
		for _, operand := range []string{"CURRENT_ROLE", "CURRENT_USER", "sa_actor"} {
			s.run(fixture.Exec("ALTER STATISTICS sa_before OWNER TO "+operand, 0))
		}
		s.run(statisticsError("ALTER STATISTICS sa_before OWNER TO sa_recipient", "42501", `must be able to SET ROLE "sa_recipient"`))
		held, err := s.conn.PrepareContext(s.pair.Ctx, "ALTER STATISTICS sa_before OWNER TO sa_recipient")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		s.run(fixture.Exec("RESET ROLE", 0))
		s.run(fixture.Exec("GRANT sa_recipient TO sa_actor WITH SET TRUE", 0))
		s.run(fixture.Exec("REVOKE CREATE ON SCHEMA public FROM PUBLIC", 0))
		s.run(fixture.Exec("SET ROLE sa_actor", 0))
		s.run(statisticsError("ALTER STATISTICS sa_before OWNER TO sa_recipient", "42501", "permission denied for schema public"))
		s.run(fixture.Exec("RESET ROLE", 0))
		s.run(fixture.Exec("GRANT CREATE ON SCHEMA public TO sa_recipient", 0))
		s.run(fixture.Exec("SET ROLE sa_actor", 0))
		backend := s.pair.Backends()[0]
		if s.pg {
			backend = s.pair.Backends()[1]
		}
		fixture.RunStep(t, s.pair.Ctx, fixture.HeldStatement{s.conn, held, "ALTER STATISTICS sa_before OWNER TO sa_recipient"}, backend.Recorder, fixture.Exec("ALTER STATISTICS sa_before OWNER TO sa_recipient", 0))
		s.run(statisticsAlterMetadata("sa_before", "public", "sa_recipient", nil))
		// An unrelated actor may name the already-current owner, but not change it.
		s.run(fixture.Exec("RESET ROLE", 0))
		s.run(fixture.Exec("SET ROLE sa_owner", 0))
		s.run(fixture.Exec("ALTER STATISTICS sa_before OWNER TO sa_recipient", 0))
		s.run(statisticsError("ALTER STATISTICS sa_before OWNER TO sa_owner", "42501", "must be owner of statistics object sa_before"))
		s.run(fixture.Exec("RESET ROLE", 0))
		// Genuine superuser transfer bypasses recipient CREATE, independently of table ownership.
		s.run(fixture.Exec("REVOKE CREATE ON SCHEMA public FROM sa_recipient", 0))
		s.run(fixture.Exec("ALTER STATISTICS sa_before OWNER TO sa_owner", 0))
		s.run(fixture.Exec("ALTER STATISTICS sa_before OWNER TO sa_recipient", 0))
		s.run(fixture.Exec(fmt.Sprintf("GRANT %q TO sa_recipient WITH SET TRUE", sessionOwner), 0))
		s.run(fixture.Exec("SET ROLE sa_recipient", 0))
		s.run(fixture.Exec("ALTER STATISTICS sa_before OWNER TO CURRENT_ROLE", 0))
		s.run(fixture.Exec("ALTER STATISTICS sa_before OWNER TO SESSION_USER", 0))
		s.run(fixture.Exec("RESET ROLE", 0))
		s.run(statisticsAlterMetadata("sa_before", "public", sessionOwner, nil))
		s.run(fixture.Exec("ALTER STATISTICS sa_before OWNER TO sa_recipient", 0))
		s.run(fixture.Exec("ALTER ROLE sa_recipient RENAME TO sa_other", 0))
		s.run(statisticsAlterMetadata("sa_before", "public", "sa_other", nil))
		fixture.RunStep(t, s.pair.Ctx, fixture.HeldStatement{s.conn, held, "ALTER STATISTICS sa_before OWNER TO sa_recipient"}, backend.Recorder, statisticsError("ALTER STATISTICS sa_before OWNER TO sa_recipient", "42704", `role "sa_recipient" does not exist`))
		s.run(fixture.Exec("SET ROLE sa_other", 0))
		s.run(fixture.Exec("ALTER STATISTICS sa_before OWNER TO CURRENT_USER", 0))
		s.run(fixture.Exec("RESET ROLE", 0))
		statisticsAlterFacts(t, s, "sa_before")
	})
}
