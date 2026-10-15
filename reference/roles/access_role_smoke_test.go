package reference_test

import "testing"

func TestReferenceAccessRoleFeatureSmoke(t *testing.T) {
	accessRoleCases(t, []string{"ar_actor", "ar_middle", "ar_target"}, func(s *membershipScenario) {
		accessRoleGraph(s)
		actor, target := accessRoleOID(s, "ar_actor"), accessRoleOID(s, "ar_target")
		accessRoleTable(s)
		s.exec("ALTER TABLE ar_t ENABLE ROW LEVEL SECURITY")
		s.actor("ar_actor")
		accessRoleVector(s, "ar_actor", "ar_target", true, false, false, true)
		accessRoleBool(s, "SELECT pg_has_role('ar_target','MEMBER') AS v", true)
		accessRoleBool(s, "SELECT pg_has_role('ar_actor'::name,$1::oid,'USAGE') AS v", false, target)
		accessRoleBool(s, "SELECT pg_has_role($1::oid,'ar_target'::name,'SET') AS v", false, actor)
		accessRoleBool(s, "SELECT pg_has_role($1::oid,$2::oid,'USAGE WITH GRANT OPTION') AS v", true, actor, target)
		accessRoleBool(s, "SELECT pg_has_role(NULL::name,'missing','bad') AS v", nil)
		accessRoleBool(s, "SELECT row_security_active(NULL::oid) AS v", nil)
		held := predefinedHeld(s, "SELECT row_security_active($1::text) AS v")
		accessRoleHeldBool(s, held, true, "ar_t")
		s.actor("ar_target")
		accessRoleHeldBool(s, held, false, "ar_t")
		s.exec("ALTER TABLE ar_t FORCE ROW LEVEL SECURITY")
		accessRoleHeldBool(s, held, true, "ar_t")
		s.actor("")
		accessRoleHeldBool(s, held, false, "ar_t")
		s.exec("ALTER TABLE ar_t DISABLE ROW LEVEL SECURITY")
		s.actor("ar_actor")
		accessRoleHeldBool(s, held, false, "ar_t")
	})
}
