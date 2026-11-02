package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsRoleKindIdentity(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pgss_kind_identity"})
	pgssCase(t, true, func(s *pgssScenario) {
		s.work(fixture.Exec("SET pg_stat_statements.track_utility=on", 0))
		queries := []string{"CREATE ROLE pgss_kind_identity", "CREATE GROUP pgss_kind_identity", "CREATE USER pgss_kind_identity"}
		s.counts("CREATE % pgss_kind_identity", 0, 0, 0)
		for i, query := range queries {
			step := fixture.Exec(query, 0)
			if i > 0 {
				step = fixture.Prepared(step)
			}
			s.work(step)
			s.both(fixture.Select("SELECT rolcanlogin,rolinherit FROM pg_roles WHERE rolname='pgss_kind_identity'", []string{"rolcanlogin", "rolinherit"}, []string{"BOOL", "BOOL"}, [][]any{{i == 2, true}}))
			s.exec("DROP ROLE pgss_kind_identity")
		}
		// CreateRoleStmt.stmt_type remains selected despite ROLE/GROUP
		// having equivalent default catalog effects.
		for _, query := range queries {
			s.entry(query, 1, 0)
		}
		s.counts("CREATE % pgss_kind_identity", 3, 3, 0)
	})
}
