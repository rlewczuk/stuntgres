package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRLSPolicyOwnership(t *testing.T) {
	membershipCases(t, []string{"policy_owner", "policy_member", "policy_grantee", "policy_schema", "policy_set", "policy_ephemeral"}, func(s *membershipScenario) {
		s.actor("policy_owner")
		fixture.WithPublicSchemaOwner(s.t, s.p, s.backend, "policy_owner", func() { policySetup(s) })
		s.exec("CREATE POLICY p ON t")
		s.actor("")
		s.exec("GRANT SELECT,INSERT,UPDATE,DELETE ON t TO policy_grantee")
		s.exec("ALTER ROLE policy_grantee CREATEROLE")
		s.exec("GRANT policy_owner TO policy_member WITH INHERIT TRUE, SET FALSE")
		s.exec("GRANT policy_owner TO policy_set WITH INHERIT FALSE, SET TRUE")
		for _, role := range []string{"policy_grantee", "policy_set"} {
			s.actor(role)
			for _, q := range []string{"CREATE POLICY q ON t", "ALTER POLICY p ON t USING(TRUE)", "ALTER POLICY p ON t RENAME TO q"} {
				policyError(s, q, "42501", "must be owner of table t", "", 0)
			}
			policyError(s, "DROP POLICY p ON t", "42501", "must be owner of relation t", "", 0)
			s.exec("DROP POLICY IF EXISTS absent ON t")
			policyNotice(s, fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `policy "absent" for relation "t" does not exist, skipping`})
		}
		s.actor("policy_member")
		s.exec("ALTER POLICY p ON t USING(TRUE)")
		s.exec("CREATE POLICY inherited ON t")
		s.exec("DROP POLICY inherited ON t")
		// Prepare cannot cache successful inherited ownership.
		policyHeld(s, "ALTER POLICY p ON t USING(FALSE)", func(stmt *sql.Stmt) {
			s.actor("")
			s.exec("REVOKE INHERIT OPTION FOR policy_owner FROM policy_member")
			s.actor("policy_member")
			fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.c, Statement: stmt, SQL: "ALTER POLICY p ON t USING(FALSE)"}, s.record, fixture.ImplicitError("ALTER POLICY p ON t USING(FALSE)", fixture.Diagnostic{Code: "42501", Message: "must be owner of table t"}))
		})
		s.actor("")
		s.exec("ALTER SCHEMA public OWNER TO policy_schema")
		s.actor("policy_schema")
		policyError(s, "DROP POLICY p ON t", "42501", "must be owner of relation t", "", 0)
		s.actor("")
		s.exec("ALTER SCHEMA public OWNER TO " + s.bootstrap)
		// Held CREATE resolves role existence at execution, not description.
		policyHeld(s, "CREATE POLICY fresh ON t TO policy_ephemeral", func(stmt *sql.Stmt) {
			s.exec("DROP ROLE policy_ephemeral")
			fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.c, Statement: stmt, SQL: "CREATE POLICY fresh ON t TO policy_ephemeral"}, s.record, fixture.ImplicitError("CREATE POLICY fresh ON t TO policy_ephemeral", fixture.Diagnostic{Code: "42704", Message: `role "policy_ephemeral" does not exist`}))
			s.exec("CREATE ROLE policy_ephemeral")
			fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.c, Statement: stmt, SQL: "CREATE POLICY fresh ON t TO policy_ephemeral"}, s.record, fixture.Exec("CREATE POLICY fresh ON t TO policy_ephemeral", 0))
		})
		s.exec("DROP POLICY fresh ON t")
		s.exec("DROP ROLE policy_ephemeral")
		// Restore authority, then revoke the membership itself: both direct and
		// held execution must consult current ownership authority.
		s.exec("GRANT policy_owner TO policy_member WITH INHERIT TRUE, SET FALSE")
		s.actor("policy_member")
		s.exec("ALTER POLICY p ON t")
		policyHeld(s, "ALTER POLICY p ON t", func(stmt *sql.Stmt) {
			s.actor("")
			s.exec("REVOKE policy_owner FROM policy_member")
			s.actor("policy_member")
			fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.c, Statement: stmt, SQL: "ALTER POLICY p ON t"}, s.record, fixture.ImplicitError("ALTER POLICY p ON t", fixture.Diagnostic{Code: "42501", Message: "must be owner of table t"}))
		})
		policyError(s, "ALTER POLICY p ON t", "42501", "must be owner of table t", "", 0)
		s.actor("")
		s.exec("DROP POLICY p ON t")
		policyRows(s)
	})
	membershipCases(t, []string{"policy_session", "policy_effective", "policy_extra"}, func(s *membershipScenario) {
		s.actor("policy_effective")
		fixture.WithPublicSchemaOwner(s.t, s.p, s.backend, "policy_effective", func() { policySetup(s) })
		s.actor("")
		s.exec("GRANT policy_effective TO policy_session WITH INHERIT TRUE, SET TRUE")
		s.actor("policy_session")
		s.exec("SET ROLE policy_effective")
		s.exec("CREATE POLICY p ON t TO CURRENT_USER,CURRENT_ROLE,SESSION_USER USING(current_user=current_role)")
		policyOrderedRoles(s, "p", "{policy_effective,policy_effective,policy_session}")
		policyHeld(s, "CREATE POLICY held_role ON t TO CURRENT_USER", func(stmt *sql.Stmt) {
			s.exec("RESET ROLE")
			fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.c, Statement: stmt, SQL: "CREATE POLICY held_role ON t TO CURRENT_USER"}, s.record, fixture.Exec("CREATE POLICY held_role ON t TO CURRENT_USER", 0))
		})
		s.actor("")
		policyOrderedRoles(s, "held_role", "{policy_session}")
		policyResolvedRoleExport(s)
		s.exec("ALTER POLICY p ON t TO PUBLIC")
		policyError(s, "DROP ROLE policy_session", "2BP01", `role "policy_session" cannot be dropped because some objects depend on it`, "target of policy held_role on table t", 0)
		s.exec("DROP POLICY held_role ON t")
		s.actor("policy_session")
		s.exec("SET ROLE policy_effective")
		s.exec("ALTER POLICY p ON t TO CURRENT_USER,CURRENT_ROLE,SESSION_USER")
		s.actor("")
		// Both effective and original session names are retained as targets.
		policyError(s, "DROP ROLE policy_session", "2BP01", `role "policy_session" cannot be dropped because some objects depend on it`, "target of policy p on table t", 0)
		s.exec("ALTER POLICY p ON t TO policy_extra,policy_extra")
		policyOrderedRoles(s, "p", "{policy_extra,policy_extra}")
		policyError(s, "DROP ROLE policy_extra", "2BP01", `role "policy_extra" cannot be dropped because some objects depend on it`, "target of policy p on table t\ntarget of policy p on table t", 0)
		s.exec("ALTER POLICY p ON t TO policy_extra,PUBLIC")
		policyNotice(s, fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "ignoring specified roles other than PUBLIC", Hint: "All roles are members of the PUBLIC role."})
		policyOrderedRoles(s, "p", "{public}")
		s.exec("DROP ROLE policy_extra")
		s.exec("ALTER POLICY p ON t TO PUBLIC,policy_missing")
		policyNotice(s, fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "ignoring specified roles other than PUBLIC", Hint: "All roles are members of the PUBLIC role."})
		policyError(s, "ALTER POLICY p ON t TO policy_missing,PUBLIC", "42704", `role "policy_missing" does not exist`, "", 0)
		if len(*s.notices) != 0 {
			s.t.Fatal("warning preceded failed role resolution", *s.notices)
		}
		s.exec("DROP POLICY p ON t")
		s.exec("CREATE POLICY warning ON t TO PUBLIC,policy_missing")
		policyNotice(s, fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "ignoring specified roles other than PUBLIC", Hint: "All roles are members of the PUBLIC role."})
		policyError(s, "CREATE POLICY fail_role ON t TO policy_missing,PUBLIC", "42704", `role "policy_missing" does not exist`, "", 0)
		s.exec("DROP POLICY warning ON t")
		s.exec("DROP ROLE policy_session")
	})
}
