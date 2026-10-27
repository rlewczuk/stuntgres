package reference_test

import "stuntgres/reference/fixture"

import "testing"

func TestReferenceAccessRoleFeatureSmoke(t *testing.T) {
	fixture.AccessRoleCases(t, []string{"ar_actor", "ar_middle", "ar_target"}, func(s *fixture.RoleMembershipScenario) {
		fixture.AccessRoleGraph(s)
		actor, target := fixture.AccessRoleOID(s, "ar_actor"), fixture.AccessRoleOID(s, "ar_target")
		fixture.AccessRoleTable(s)
		s.Exec("ALTER TABLE ar_t ENABLE ROW LEVEL SECURITY")
		s.Actor("ar_actor")
		fixture.AccessRoleVector(s, "ar_actor", "ar_target", true, false, false, true)
		fixture.AccessRoleBool(s, "SELECT pg_has_role('ar_target','MEMBER') AS v", true)
		fixture.AccessRoleBool(s, "SELECT pg_has_role('ar_actor'::name,$1::oid,'USAGE') AS v", false, target)
		fixture.AccessRoleBool(s, "SELECT pg_has_role($1::oid,'ar_target'::name,'SET') AS v", false, actor)
		fixture.AccessRoleBool(s, "SELECT pg_has_role($1::oid,$2::oid,'USAGE WITH GRANT OPTION') AS v", true, actor, target)
		fixture.AccessRoleBool(s, "SELECT pg_has_role(NULL::name,'missing','bad') AS v", nil)
		fixture.AccessRoleBool(s, "SELECT row_security_active(NULL::oid) AS v", nil)
		held := predefinedHeld(s, "SELECT row_security_active($1::text) AS v")
		fixture.AccessRoleHeldBool(s, held, true, "ar_t")
		s.Actor("ar_target")
		fixture.AccessRoleHeldBool(s, held, false, "ar_t")
		s.Exec("ALTER TABLE ar_t FORCE ROW LEVEL SECURITY")
		fixture.AccessRoleHeldBool(s, held, true, "ar_t")
		s.Actor("")
		fixture.AccessRoleHeldBool(s, held, false, "ar_t")
		s.Exec("ALTER TABLE ar_t DISABLE ROW LEVEL SECURITY")
		s.Actor("ar_actor")
		fixture.AccessRoleHeldBool(s, held, false, "ar_t")
	})
}
