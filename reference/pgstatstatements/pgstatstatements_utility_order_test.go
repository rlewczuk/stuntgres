package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsRawUtilityOptionOrder(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pgss_ordered_role"})
	pgssCase(t, true, func(s *pgssScenario) {
		s.work(fixture.Exec("SET pg_stat_statements.track_utility=on", 0))
		functions := []string{
			"CREATE FUNCTION pgss_part.pgss_ordered_declaration() RETURNS integer LANGUAGE sql IMMUTABLE AS $$SELECT 1$$",
			"CREATE FUNCTION pgss_part.pgss_ordered_declaration() RETURNS integer AS $$SELECT 1$$ IMMUTABLE LANGUAGE sql",
		}
		roles := []string{
			"CREATE ROLE pgss_ordered_role LOGIN INHERIT",
			"CREATE ROLE pgss_ordered_role INHERIT LOGIN",
		}
		s.counts("CREATE FUNCTION pgss_part.pgss_ordered_declaration%", 0, 0, 0)
		s.counts("CREATE ROLE pgss_ordered_role%", 0, 0, 0)
		for i := range functions {
			for _, query := range []string{functions[i], roles[i]} {
				step := fixture.Exec(query, 0)
				if i == 1 {
					step = fixture.Prepared(step)
				}
				s.work(step)
			}
			// Equivalent SQL effects do not erase the release-selected raw
			// ordered DefElem lists used for these utility identities.
			s.both(fixture.Select("SELECT pgss_part.pgss_ordered_declaration() AS value", []string{"value"}, []string{"INT4"}, [][]any{{int64(1)}}))
			s.both(fixture.Select("SELECT rolcanlogin,rolinherit FROM pg_roles WHERE rolname='pgss_ordered_role'", []string{"rolcanlogin", "rolinherit"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
			s.exec("DROP FUNCTION pgss_part.pgss_ordered_declaration()")
			s.exec("DROP ROLE pgss_ordered_role")
		}
		for _, query := range append(functions, roles...) {
			s.entry(query, 1, 0)
		}
		s.counts("CREATE FUNCTION pgss_part.pgss_ordered_declaration%", 2, 2, 0)
		s.counts("CREATE ROLE pgss_ordered_role%", 2, 2, 0)
	})
}
