package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func rlsBaseCases(t *testing.T, body func(*fixture.RoleMembershipScenario, *sql.DB)) {
	t.Helper()
	predefinedCases(t, []string{"rls_owner", "rls_reader", "rls_other", "rls_bypass", "rls_noacl", "rls_member"}, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		s.Exec("ALTER ROLE rls_bypass BYPASSRLS")
		s.Exec("ALTER ROLE rls_noacl BYPASSRLS")
		s.Actor("rls_owner")
		fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "rls_owner", func() { s.Exec("CREATE TABLE t(id INT PRIMARY KEY,v INT DEFAULT 9)") })
		s.Run(fixture.Exec("INSERT INTO t VALUES(1,10),(2,20)", 2))
		s.Exec("GRANT SELECT,INSERT,UPDATE,DELETE ON t TO rls_reader,rls_bypass,rls_member")
		body(s, db)
	})
}

func rlsBaseRows(s *fixture.RoleMembershipScenario, rows [][]any) {
	s.T.Helper()
	predefinedSelect(s, "SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, rows)
}

func rlsBaseSeedRows() [][]any { return [][]any{{int64(1)}, {int64(2)}} }

func rlsBaseEnabled(s *fixture.RoleMembershipScenario, enabled bool) {
	s.T.Helper()
	predefinedSelect(s, "SELECT rowsecurity FROM pg_tables WHERE schemaname='public' AND tablename='t'", []string{"rowsecurity"}, []string{"BOOL"}, [][]any{{enabled}})
}

func rlsBaseProposalDenied(s *fixture.RoleMembershipScenario, q string) {
	s.T.Helper()
	predefinedDenied(s, q, `new row violates row-level security policy for table "t"`, false)
}

// Every Diagnostic literal is a complete expectation: unspecified optional fields
// must be empty, not ignored. Prepare timing is asserted separately by RunStep.
func rlsBaseExpression(s *fixture.RoleMembershipScenario, q string, rows [][]any) {
	s.T.Helper()
	predefinedSelect(s, q, []string{"id"}, []string{"INT4"}, rows)
}
