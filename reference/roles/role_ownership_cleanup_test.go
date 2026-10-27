package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleOwnershipCleanup(t *testing.T) {
	ownershipCases(t, func(s *fixture.RoleMembershipScenario) {
		ownershipSeed(s, "own_cleanup", "own_other")
		s.Actor("own_other")
		s.Exec("GRANT SELECT,UPDATE ON own_cleanup TO own_source WITH GRANT OPTION")
		s.Exec("GRANT SELECT(id),UPDATE(id) ON own_cleanup TO own_source WITH GRANT OPTION")
		s.Exec("GRANT SELECT ON own_cleanup TO own_alt WITH GRANT OPTION")
		s.Exec("GRANT SELECT ON own_cleanup TO PUBLIC")
		s.Actor("own_source")
		s.Exec("GRANT SELECT,UPDATE ON own_cleanup TO own_downstream WITH GRANT OPTION")
		s.Exec("GRANT SELECT(id),UPDATE(id) ON own_cleanup TO own_reader")
		s.Actor("own_downstream")
		s.Exec("GRANT SELECT,UPDATE ON own_cleanup TO own_reader")
		s.Actor("own_alt")
		s.Exec("GRANT SELECT ON own_cleanup TO own_downstream")
		s.Actor("")
		s.Exec("GRANT own_alt TO own_downstream WITH INHERIT TRUE, SET FALSE")
		s.Exec("GRANT own_middle TO own_source WITH ADMIN TRUE")
		s.Exec("GRANT own_middle TO own_downstream WITH ADMIN TRUE GRANTED BY own_source")
		s.Exec("GRANT own_middle TO own_reader GRANTED BY own_downstream")
		s.Exec("GRANT own_middle TO own_alt WITH ADMIN TRUE")
		s.Exec("GRANT own_middle TO own_reader GRANTED BY own_alt")
		s.Exec("CREATE POLICY own_policy_mixed ON own_cleanup TO own_source,own_reader,own_source USING(id>0)")
		s.Exec("CREATE POLICY own_policy_last ON own_cleanup TO own_source USING(id>0)")
		s.Exec("CREATE POLICY own_policy_public ON own_cleanup TO PUBLIC USING(id>0)")
		s.Exec("REASSIGN OWNED BY own_source TO own_target")
		// REASSIGN must not remove received grants or grantor-only memberships.
		ownershipRoleDependency(s, "own_source", "privileges for table own_cleanup", "privileges for column id of table own_cleanup", "target of policy own_policy_mixed on table own_cleanup", "target of policy own_policy_mixed on table own_cleanup", "target of policy own_policy_last on table own_cleanup", "privileges for membership of role own_downstream in role own_middle")
		s.Exec("ALTER TABLE own_cleanup ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY")
		s.Actor("own_source")
		ownershipPolicyRows(s, "SELECT id FROM own_cleanup", []string{"id"}, [][]any{{int64(7)}})
		s.Actor("")
		s.ClearNotices()
		s.Exec("DROP OWNED BY own_source RESTRICT")
		ownershipNotices(s)
		ownershipRows(s, "own_cleanup")
		ownershipOwner(s, "own_cleanup", "own_other")
		s.Actor("own_reader")
		ownershipPolicyRows(s, "SELECT id FROM own_cleanup", []string{"id"}, [][]any{{int64(7)}}) // PUBLIC survives independently.
		step := fixture.RoleReferenceError("UPDATE own_cleanup SET id=7", "42501", "permission denied for table own_cleanup", "", s.Prepared, "execute")
		s.Run(step)
		s.Actor("own_downstream")
		ownershipPolicyRows(s, "SELECT id FROM own_cleanup", []string{"id"}, [][]any{{int64(7)}}) // Alternate grantor also survives.
		s.Run(step)
		s.Actor("")
		// The direct source-grantor edge is removed, but own_alt still provides
		// an independent indirect membership path to own_middle.
		ownershipInherited(s, "own_downstream", "own_middle", "MEMBER", true)
		s.Flags("own_middle", "own_downstream", "own_source", 0, false, false, false)
		ownershipInherited(s, "own_reader", "own_middle", "MEMBER", true)
		ownershipInherited(s, "own_source", "own_middle", "MEMBER", true)
		if s.Backend.PG {
			ownershipSelect(s, "SELECT policyname,roles::TEXT AS roles FROM pg_policies WHERE tablename='own_cleanup' ORDER BY policyname", []string{"policyname", "roles"}, []string{"NAME", "TEXT"}, [][]any{{"own_policy_mixed", "{own_reader}"}, {"own_policy_public", "{public}"}})
		}
		ownershipFailure(s, "ALTER POLICY own_policy_last ON own_cleanup", fixture.Diagnostic{Code: "42704", Message: `policy "own_policy_last" for table "own_cleanup" does not exist`})
		s.Exec("DROP POLICY own_policy_public ON own_cleanup")
		s.Exec("GRANT SELECT,INSERT ON own_cleanup TO own_source")
		s.Actor("own_source")
		ownershipSelect(s, "SELECT id FROM own_cleanup", []string{"id"}, []string{"INT4"}, nil)
		ownershipFailure(s, "INSERT INTO own_cleanup VALUES(9)", fixture.Diagnostic{Code: "42501", Message: `new row violates row-level security policy for table "own_cleanup"`})
		s.Actor("own_reader")
		ownershipPolicyRows(s, "SELECT id FROM own_cleanup", []string{"id"}, [][]any{{int64(7)}})
		s.Actor("")
		s.Exec("REVOKE SELECT,INSERT ON own_cleanup FROM own_source")
		s.Exec("ALTER TABLE own_cleanup DISABLE ROW LEVEL SECURITY")
		ownershipRows(s, "own_cleanup")
		s.Actor("own_other")
		s.Exec("REVOKE SELECT ON own_cleanup FROM PUBLIC")
		s.Actor("own_reader")
		ownershipRows(s, "own_cleanup")
		s.Actor("own_downstream")
		s.Exec("GRANT SELECT ON own_cleanup TO own_actor")
		s.Actor("own_actor")
		ownershipRows(s, "own_cleanup")
		s.Actor("")
		s.Exec("DROP ROLE own_source")
		// Endpoint membership disappears with DROP ROLE; cleanup itself retained it.
		ownershipBool(s, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='own_source')", false)
		s.Exec("REVOKE own_alt FROM own_downstream")
		ownershipInherited(s, "own_downstream", "own_middle", "MEMBER", false)
	})
}

func TestReferenceRoleOwnershipDropRole(t *testing.T) {
	ownershipCases(t, func(s *fixture.RoleMembershipScenario) {
		ownershipSeed(s, "own_role_table", "own_source")
		s.Actor("own_source")
		fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "own_source", func() {
			s.Exec("CREATE INDEX own_role_index ON own_role_table(id)")
			s.Exec("CREATE TYPE own_role_enum AS ENUM('a')")
			s.Exec("CREATE DOMAIN own_role_domain AS INT")
			s.Exec("CREATE TYPE own_role_composite AS(id INT)")
		})
		s.Actor("")
		ownershipRoleDependency(s, "own_source", "owner of table own_role_table", "owner of type own_role_enum", "owner of type own_role_domain", "owner of type own_role_composite")
		s.Exec("REASSIGN OWNED BY own_source TO own_target")
		ownershipSeed(s, "own_role_grants", "own_other")
		s.Exec("GRANT SELECT ON own_role_grants TO own_source")
		ownershipRoleDependency(s, "own_source", "privileges for table own_role_grants")
		s.Exec("REVOKE SELECT ON own_role_grants FROM own_source")
		s.Exec("GRANT UPDATE(id) ON own_role_grants TO own_source")
		ownershipRoleDependency(s, "own_source", "privileges for column id of table own_role_grants")
		s.Exec("REVOKE UPDATE(id) ON own_role_grants FROM own_source")
		s.Exec("CREATE POLICY own_role_policy ON own_role_grants TO own_source USING(id>0)")
		ownershipRoleDependency(s, "own_source", "target of policy own_role_policy on table own_role_grants")
		s.Exec("DROP POLICY own_role_policy ON own_role_grants")
		s.Exec("GRANT own_middle TO own_source WITH ADMIN TRUE")
		s.Exec("GRANT own_middle TO own_reader GRANTED BY own_source")
		ownershipRoleDependency(s, "own_source", "privileges for membership of role own_reader in role own_middle")
		s.Exec("DROP OWNED BY own_source")
		ownershipInherited(s, "own_reader", "own_middle", "MEMBER", false)
		// Complete multi-role preflight: early role and its endpoints survive a
		// later role's ownership refusal; no partially removed membership graph.
		s.Exec("GRANT own_source TO own_actor")
		var oldID int64
		if err := s.C.QueryRowContext(s.P.Ctx, "SELECT oid FROM pg_roles WHERE rolname='own_source'").Scan(&oldID); err != nil || oldID <= 0 {
			s.T.Fatal(oldID, err)
		}
		ownershipFailure(s, "DROP ROLE own_source,own_target", fixture.Diagnostic{Code: "2BP01", Message: `role "own_target" cannot be dropped because some objects depend on it`, Detail: "owner of table own_role_table\nowner of type own_role_enum\nowner of type own_role_domain\nowner of type own_role_composite"})
		ownershipInherited(s, "own_actor", "own_source", "MEMBER", true)
		s.Exec("DROP ROLE own_source")
		s.Exec("CREATE ROLE own_source LOGIN")
		var freshID int64
		if err := s.C.QueryRowContext(s.P.Ctx, "SELECT oid FROM pg_roles WHERE rolname='own_source'").Scan(&freshID); err != nil || freshID <= 0 || freshID == oldID {
			s.T.Fatal("recreated role identity", oldID, freshID, err)
		}
		ownershipInherited(s, "own_actor", "own_source", "MEMBER", false)
		s.Exec(`CREATE SCHEMA "own Quoted Schema" AUTHORIZATION own_source`)
		s.Actor("own_source")
		s.Exec(`CREATE TABLE "own Quoted Schema"."Odd Table"(id INT)`)
		s.Run(fixture.Exec(`INSERT INTO "own Quoted Schema"."Odd Table" VALUES(7)`, 1))
		s.Actor("")
		ownershipRoleDependency(s, "own_source", `owner of schema own Quoted Schema`, `owner of table "own Quoted Schema"."Odd Table"`)
		s.Exec(`SET search_path TO "own Quoted Schema",public`)
		ownershipRoleDependency(s, "own_source", `owner of schema own Quoted Schema`, `owner of table "Odd Table"`)
		ownershipRows(s, `"Odd Table"`)
		s.Exec("DROP OWNED BY own_source")
		s.Exec("SET search_path TO public")
		ownershipFailure(s, "DROP ROLE own_absent", fixture.Diagnostic{Code: "42704", Message: `role "own_absent" does not exist`})
		ownershipFailure(s, "DROP ROLE "+s.Bootstrap, fixture.Diagnostic{Code: "55006", Message: "current user cannot be dropped"})
		s.Exec("ALTER ROLE own_target SUPERUSER")
		s.Exec("SET ROLE own_target")
		ownershipFailure(s, "DROP ROLE own_target", fixture.Diagnostic{Code: "55006", Message: "current user cannot be dropped"})
		ownershipFailure(s, "DROP ROLE "+s.Bootstrap, fixture.Diagnostic{Code: "55006", Message: "session user cannot be dropped"})
		s.Exec("RESET ROLE")
		ownershipFailure(s, "DROP ROLE pg_database_owner", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop role pg_database_owner because it is required by the database system"})
		s.Exec("DROP OWNED BY own_target CASCADE")
		s.Exec("DROP ROLE own_target")
	})
}
