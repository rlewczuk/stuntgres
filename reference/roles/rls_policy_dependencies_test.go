package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func policyRestrict(s *fixture.RoleMembershipScenario, q, message, detail string) {
	s.T.Helper()
	step := fixture.ImplicitError(q, fixture.Diagnostic{Code: "2BP01", Message: message, Detail: detail, Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
	step.ErrorStage = "execute"
	s.Run(step)
}

func TestReferenceRLSPolicyDependencies(t *testing.T) {
	t.Run("mixed_closure", func(t *testing.T) {
		fixture.RoleMembershipCases(t, nil, func(s *fixture.RoleMembershipScenario) {
			policySetup(s)
			s.Exec("CREATE TABLE helper(id INTEGER)")
			s.Exec("CREATE VIEW dependent_view AS SELECT id FROM helper")
			s.Exec("CREATE POLICY p ON t USING(EXISTS(SELECT 1 FROM helper))")
			policyRestrict(s, "DROP TABLE helper", "cannot drop table helper because other objects depend on it", "view dependent_view depends on table helper\npolicy p on table t depends on table helper")
			s.Exec("DROP TABLE helper CASCADE")
			policyNotice(s, fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to 2 other objects", Detail: "drop cascades to view dependent_view\ndrop cascades to policy p on table t"})
			policyError(s, "ALTER POLICY p ON t", "42704", `policy "p" for table "t" does not exist`, "", 0)
			policyRows(s)
		})
	})
	fixture.RoleMembershipCases(t, []string{"policy_target"}, func(s *fixture.RoleMembershipScenario) {
		policySetup(s)
		s.Exec("CREATE TABLE helper(id INTEGER)")
		s.Exec("CREATE TABLE retained(id INTEGER)")
		s.Exec("CREATE POLICY p ON t TO policy_target USING(EXISTS(SELECT 1 FROM helper WHERE helper.id=t.id)) WITH CHECK(EXISTS(SELECT 1 FROM retained WHERE retained.id=t.id))")
		policyRestrict(s, "DROP TABLE helper", "cannot drop table helper because other objects depend on it", "policy p on table t depends on table helper")
		policyRestrict(s, "DROP TABLE retained", "cannot drop table retained because other objects depend on it", "policy p on table t depends on table retained")
		s.Exec("ALTER POLICY p ON t USING(TRUE)")
		s.Exec("DROP TABLE helper")
		policyRestrict(s, "DROP TABLE retained", "cannot drop table retained because other objects depend on it", "policy p on table t depends on table retained")
		policyRoleBlocked(s, "policy_target", "p")
		s.Exec("ALTER POLICY p ON t WITH CHECK(TRUE)")
		s.Exec("DROP TABLE retained")
		policyRoleBlocked(s, "policy_target", "p")
		s.Exec("ALTER POLICY p ON t TO PUBLIC")
		s.Exec("DROP ROLE policy_target")
		s.Exec("DROP POLICY p ON t CASCADE")
		policyRows(s)
	})
	for _, behavior := range []string{"CASCADE", "RESTRICT"} {
		t.Run(behavior, func(t *testing.T) {
			fixture.RoleMembershipCases(t, []string{"policy_drop_target"}, func(s *fixture.RoleMembershipScenario) {
				policySetup(s)
				s.Exec("CREATE TABLE helper(id INTEGER)")
				s.Exec("CREATE POLICY p ON t TO policy_drop_target USING(EXISTS(SELECT 1 FROM helper))")
				s.Exec("DROP POLICY p ON t " + behavior)
				s.Exec("DROP ROLE policy_drop_target")
				s.Run(fixture.Exec("INSERT INTO helper VALUES(7)", 1))
				policyRows(s)
			})
		})
	}
	fixture.RoleMembershipCases(t, []string{"policy_table_target"}, func(s *fixture.RoleMembershipScenario) {
		policySetup(s)
		s.Exec("CREATE POLICY p ON t TO policy_table_target USING(id>0)")
		policyColumnBlocked(s, "id", "p")
		policyRestrict(s, "ALTER TABLE t DROP COLUMN id", "cannot drop column id of table t because other objects depend on it", "policy p on table t depends on column id of table t")
		s.Exec("ALTER TABLE t DROP COLUMN id CASCADE")
		policyNotice(s, fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to policy p on table t"})
		policyError(s, "ALTER POLICY p ON t", "42704", `policy "p" for table "t" does not exist`, "", 0)
		s.Exec("DROP ROLE policy_table_target")
		s.Run(fixture.Select("SELECT allowed FROM t ORDER BY allowed", []string{"allowed"}, []string{"BOOL"}, [][]any{{false}, {true}}))
	})
	fixture.RoleMembershipCases(t, []string{"policy_auto_target"}, func(s *fixture.RoleMembershipScenario) {
		policySetup(s)
		s.Exec("CREATE TABLE helper(id INTEGER)")
		s.Exec("CREATE POLICY p ON t TO policy_auto_target USING(EXISTS(SELECT 1 FROM helper))")
		s.Exec("DROP TABLE t")
		s.Exec("DROP ROLE policy_auto_target")
		s.Exec("DROP TABLE helper")
	})
	providers := []struct{ name, setup, expr, drop, message, detail string }{
		{"table", "CREATE TABLE helper(id INTEGER)", "EXISTS(SELECT 1 FROM helper)", "DROP TABLE helper", "cannot drop table helper because other objects depend on it", "policy p on table t depends on table helper"},
		{"view", "CREATE VIEW helper AS SELECT id FROM u", "EXISTS(SELECT 1 FROM helper)", "DROP VIEW helper", "cannot drop view helper because other objects depend on it", "policy p on table t depends on view helper"},
		{"domain", "CREATE DOMAIN policy_domain AS INTEGER", "id::policy_domain>0", "DROP DOMAIN policy_domain", "cannot drop type policy_domain because other objects depend on it", "policy p on table t depends on type policy_domain"},
		{"collation", "CREATE COLLATION policy_collation FROM \"C\"", "'a' COLLATE policy_collation='a'", "DROP COLLATION policy_collation", "cannot drop collation policy_collation because other objects depend on it", "policy p on table t depends on collation policy_collation"},
		{"sequence", "CREATE SEQUENCE policy_sequence", "nextval('policy_sequence')>0", "DROP SEQUENCE policy_sequence", "cannot drop sequence policy_sequence because other objects depend on it", "policy p on table t depends on sequence policy_sequence"},
	}
	for _, tc := range providers {
		t.Run(tc.name, func(t *testing.T) {
			fixture.RoleMembershipCases(t, nil, func(s *fixture.RoleMembershipScenario) {
				policySetup(s)
				s.Exec(tc.setup)
				s.Exec("CREATE POLICY p ON t USING(" + tc.expr + ")")
				policyRestrict(s, tc.drop, tc.message, tc.detail)
				s.Exec(tc.drop + " CASCADE")
				policyNotice(s, fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to policy p on table t"})
				policyError(s, "ALTER POLICY p ON t", "42704", `policy "p" for table "t" does not exist`, "", 0)
				policyRows(s)
			})
		})
	}
}
