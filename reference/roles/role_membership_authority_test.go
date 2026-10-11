package reference_test

import "testing"

func TestReferenceRoleMembershipAuthorityGrantor(t *testing.T) {
	membershipCases(t, []string{"ma_owner", "ma_admin", "ma_actor", "ma_member", "ma_other", "ma_far"}, func(s *membershipScenario) {
		s.owned("ma_owner")
		s.exec("GRANT ma_owner TO ma_admin WITH ADMIN TRUE, INHERIT FALSE, SET FALSE")
		s.actor("ma_admin")
		s.read(false)
		s.fail("SET ROLE ma_owner", "42501", `permission denied to set role "ma_owner"`, "")
		s.exec("GRANT ma_owner TO ma_member")
		s.actor("ma_member")
		message, detail := membershipDenied("ma_owner", "grant")
		s.fail("GRANT ma_owner TO ma_other", "42501", message, detail)
		message, detail = membershipDenied("ma_owner", "revoke")
		s.fail("REVOKE ma_owner FROM ma_other", "42501", message, detail)
		message, detail = membershipDenied("ma_member", "grant")
		s.fail("GRANT ma_member TO ma_other", "42501", message, detail)
		s.actor("")
		s.flags("ma_owner", "ma_member", "ma_admin", 1, false, true, true)
		s.exec("GRANT ma_admin TO ma_actor")
		s.actor("ma_actor")
		s.exec("GRANT ma_owner TO ma_other")
		s.actor("")
		s.flags("ma_owner", "ma_other", "ma_admin", 1, false, true, true)
		s.exec("GRANT ma_owner TO ma_actor WITH ADMIN TRUE")
		s.actor("ma_actor")
		s.exec("GRANT ma_owner TO ma_other")
		s.actor("")
		s.flags("ma_owner", "ma_other", "ma_actor", 1, false, true, true)
		s.exec("GRANT ma_owner TO ma_other WITH INHERIT FALSE, SET FALSE")
		s.flags("ma_owner", "ma_other", s.bootstrap, 1, false, false, false)
		s.exec("REVOKE ma_owner FROM ma_other GRANTED BY ma_actor")
		s.actor("ma_other")
		s.read(true)
		s.actor("")
		s.exec("REVOKE ma_owner FROM ma_other GRANTED BY ma_admin")
		s.actor("ma_other")
		s.read(false)
		s.actor("")
		s.fail("GRANT ma_owner TO ma_other GRANTED BY ma_far", "42501", `permission denied to grant privileges as role "ma_far"`, `The grantor must have the ADMIN option on role "ma_owner".`)
		s.actor("ma_admin")
		s.fail("GRANT ma_owner TO ma_other GRANTED BY ma_far", "42501", `permission denied to grant privileges as role "ma_far"`, `Only roles with privileges of role "ma_far" may grant privileges as this role.`)
		s.fail("REVOKE ma_owner FROM ma_other GRANTED BY ma_far", "42501", `permission denied to revoke privileges granted by role "ma_far"`, `Only roles with privileges of role "ma_far" may revoke privileges granted by this role.`)
	})
}

func TestReferenceRoleMembershipNoninheritAdminCorner(t *testing.T) {
	membershipCases(t, []string{"mc_role", "mc_admin", "mc_actor", "mc_member"}, func(s *membershipScenario) {
		s.exec("GRANT mc_role TO mc_admin WITH ADMIN TRUE")
		s.exec("GRANT mc_admin TO mc_actor WITH INHERIT FALSE")
		s.actor("mc_actor")
		// Outer MEMBER authority and inferred INHERIT grantor selection differ.
		s.fail("GRANT mc_role TO mc_member", "XX000", "no possible grantors", "")
		s.fail("GRANT mc_role TO mc_member GRANTED BY mc_admin", "42501", `permission denied to grant privileges as role "mc_admin"`, `Only roles with privileges of role "mc_admin" may grant privileges as this role.`)
		s.fail("GRANT mc_role TO mc_member GRANTED BY CURRENT_USER", "42501", `permission denied to grant privileges as role "mc_actor"`, `The grantor must have the ADMIN option on role "mc_role".`)
	})
}

func TestReferenceRoleMembershipNearestGrantor(t *testing.T) {
	membershipCases(t, []string{"mn_role", "mn_near", "mn_far", "mn_actor", "mn_member"}, func(s *membershipScenario) {
		s.exec("GRANT mn_role TO mn_near,mn_far WITH ADMIN TRUE")
		s.exec("GRANT mn_far TO mn_near")
		s.exec("GRANT mn_near TO mn_actor")
		s.actor("mn_actor")
		s.exec("GRANT mn_role TO mn_member")
		s.actor("")
		s.flags("mn_role", "mn_member", "mn_near", 1, false, true, true)
		s.flags("mn_role", "mn_member", "mn_far", 0, false, false, false)
	})
}
