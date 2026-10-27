package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRLSPolicyOwnership(t *testing.T) {
	fixture.RoleMembershipCases(t, []string{"policy_owner", "policy_member", "policy_grantee", "policy_schema", "policy_set", "policy_ephemeral"}, func(s *fixture.RoleMembershipScenario) {
		s.Actor("policy_owner")
		fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "policy_owner", func() { policySetup(s) })
		s.Exec("CREATE POLICY p ON t")
		s.Actor("")
		s.Exec("GRANT SELECT,INSERT,UPDATE,DELETE ON t TO policy_grantee")
		s.Exec("ALTER ROLE policy_grantee CREATEROLE")
		s.Exec("GRANT policy_owner TO policy_member WITH INHERIT TRUE, SET FALSE")
		s.Exec("GRANT policy_owner TO policy_set WITH INHERIT FALSE, SET TRUE")
		for _, role := range []string{"policy_grantee", "policy_set"} {
			s.Actor(role)
			for _, q := range []string{"CREATE POLICY q ON t", "ALTER POLICY p ON t USING(TRUE)", "ALTER POLICY p ON t RENAME TO q"} {
				policyError(s, q, "42501", "must be owner of table t", "", 0)
			}
			policyError(s, "DROP POLICY p ON t", "42501", "must be owner of relation t", "", 0)
			s.Exec("DROP POLICY IF EXISTS absent ON t")
			policyNotice(s, fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `policy "absent" for relation "t" does not exist, skipping`})
		}
		s.Actor("policy_member")
		s.Exec("ALTER POLICY p ON t USING(TRUE)")
		s.Exec("CREATE POLICY inherited ON t")
		s.Exec("DROP POLICY inherited ON t")
		// Prepare cannot cache successful inherited ownership.
		policyHeld(s, "ALTER POLICY p ON t USING(FALSE)", func(stmt *sql.Stmt) {
			s.Actor("")
			s.Exec("REVOKE INHERIT OPTION FOR policy_owner FROM policy_member")
			s.Actor("policy_member")
			fixture.RunStep(s.T, s.P.Ctx, fixture.HeldStatement{Executor: s.C, Statement: stmt, SQL: "ALTER POLICY p ON t USING(FALSE)"}, s.Record, fixture.ImplicitError("ALTER POLICY p ON t USING(FALSE)", fixture.Diagnostic{Code: "42501", Message: "must be owner of table t"}))
		})
		s.Actor("")
		s.Exec("ALTER SCHEMA public OWNER TO policy_schema")
		s.Actor("policy_schema")
		policyError(s, "DROP POLICY p ON t", "42501", "must be owner of relation t", "", 0)
		s.Actor("")
		s.Exec("ALTER SCHEMA public OWNER TO " + s.Bootstrap)
		// Held CREATE resolves role existence at execution, not description.
		policyHeld(s, "CREATE POLICY fresh ON t TO policy_ephemeral", func(stmt *sql.Stmt) {
			s.Exec("DROP ROLE policy_ephemeral")
			fixture.RunStep(s.T, s.P.Ctx, fixture.HeldStatement{Executor: s.C, Statement: stmt, SQL: "CREATE POLICY fresh ON t TO policy_ephemeral"}, s.Record, fixture.ImplicitError("CREATE POLICY fresh ON t TO policy_ephemeral", fixture.Diagnostic{Code: "42704", Message: `role "policy_ephemeral" does not exist`}))
			s.Exec("CREATE ROLE policy_ephemeral")
			fixture.RunStep(s.T, s.P.Ctx, fixture.HeldStatement{Executor: s.C, Statement: stmt, SQL: "CREATE POLICY fresh ON t TO policy_ephemeral"}, s.Record, fixture.Exec("CREATE POLICY fresh ON t TO policy_ephemeral", 0))
		})
		s.Exec("DROP POLICY fresh ON t")
		s.Exec("DROP ROLE policy_ephemeral")
		// Restore authority, then revoke the membership itself: both direct and
		// held execution must consult current ownership authority.
		s.Exec("GRANT policy_owner TO policy_member WITH INHERIT TRUE, SET FALSE")
		s.Actor("policy_member")
		s.Exec("ALTER POLICY p ON t")
		policyHeld(s, "ALTER POLICY p ON t", func(stmt *sql.Stmt) {
			s.Actor("")
			s.Exec("REVOKE policy_owner FROM policy_member")
			s.Actor("policy_member")
			fixture.RunStep(s.T, s.P.Ctx, fixture.HeldStatement{Executor: s.C, Statement: stmt, SQL: "ALTER POLICY p ON t"}, s.Record, fixture.ImplicitError("ALTER POLICY p ON t", fixture.Diagnostic{Code: "42501", Message: "must be owner of table t"}))
		})
		policyError(s, "ALTER POLICY p ON t", "42501", "must be owner of table t", "", 0)
		s.Actor("")
		s.Exec("DROP POLICY p ON t")
		policyRows(s)
	})
	fixture.RoleMembershipCases(t, []string{"policy_session", "policy_effective", "policy_extra"}, func(s *fixture.RoleMembershipScenario) {
		s.Actor("policy_effective")
		fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "policy_effective", func() { policySetup(s) })
		s.Actor("")
		s.Exec("GRANT policy_effective TO policy_session WITH INHERIT TRUE, SET TRUE")
		s.Actor("policy_session")
		s.Exec("SET ROLE policy_effective")
		s.Exec("CREATE POLICY p ON t TO CURRENT_USER,CURRENT_ROLE,SESSION_USER USING(current_user=current_role)")
		policyOrderedRoles(s, "p", "{policy_effective,policy_effective,policy_session}")
		policyHeld(s, "CREATE POLICY held_role ON t TO CURRENT_USER", func(stmt *sql.Stmt) {
			s.Exec("RESET ROLE")
			fixture.RunStep(s.T, s.P.Ctx, fixture.HeldStatement{Executor: s.C, Statement: stmt, SQL: "CREATE POLICY held_role ON t TO CURRENT_USER"}, s.Record, fixture.Exec("CREATE POLICY held_role ON t TO CURRENT_USER", 0))
		})
		s.Actor("")
		policyOrderedRoles(s, "held_role", "{policy_session}")
		policyResolvedRoleExport(s)
		s.Exec("ALTER POLICY p ON t TO PUBLIC")
		policyError(s, "DROP ROLE policy_session", "2BP01", `role "policy_session" cannot be dropped because some objects depend on it`, "target of policy held_role on table t", 0)
		s.Exec("DROP POLICY held_role ON t")
		s.Actor("policy_session")
		s.Exec("SET ROLE policy_effective")
		s.Exec("ALTER POLICY p ON t TO CURRENT_USER,CURRENT_ROLE,SESSION_USER")
		s.Actor("")
		// Both effective and original session names are retained as targets.
		policyError(s, "DROP ROLE policy_session", "2BP01", `role "policy_session" cannot be dropped because some objects depend on it`, "target of policy p on table t", 0)
		s.Exec("ALTER POLICY p ON t TO policy_extra,policy_extra")
		policyOrderedRoles(s, "p", "{policy_extra,policy_extra}")
		policyError(s, "DROP ROLE policy_extra", "2BP01", `role "policy_extra" cannot be dropped because some objects depend on it`, "target of policy p on table t\ntarget of policy p on table t", 0)
		s.Exec("ALTER POLICY p ON t TO policy_extra,PUBLIC")
		policyNotice(s, fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "ignoring specified roles other than PUBLIC", Hint: "All roles are members of the PUBLIC role."})
		policyOrderedRoles(s, "p", "{public}")
		s.Exec("DROP ROLE policy_extra")
		s.Exec("ALTER POLICY p ON t TO PUBLIC,policy_missing")
		policyNotice(s, fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "ignoring specified roles other than PUBLIC", Hint: "All roles are members of the PUBLIC role."})
		policyError(s, "ALTER POLICY p ON t TO policy_missing,PUBLIC", "42704", `role "policy_missing" does not exist`, "", 0)
		if len(*s.Notices) != 0 {
			s.T.Fatal("warning preceded failed role resolution", *s.Notices)
		}
		s.Exec("DROP POLICY p ON t")
		s.Exec("CREATE POLICY warning ON t TO PUBLIC,policy_missing")
		policyNotice(s, fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "ignoring specified roles other than PUBLIC", Hint: "All roles are members of the PUBLIC role."})
		policyError(s, "CREATE POLICY fail_role ON t TO policy_missing,PUBLIC", "42704", `role "policy_missing" does not exist`, "", 0)
		s.Exec("DROP POLICY warning ON t")
		s.Exec("DROP ROLE policy_session")
	})
}
