package reference_test

import "stuntgres/reference/fixture"

import "testing"

func TestReferenceRoleMembershipAuthorityGrantor(t *testing.T) {
	fixture.RoleMembershipCases(t, []string{"ma_owner", "ma_admin", "ma_actor", "ma_member", "ma_other", "ma_far"}, func(s *fixture.RoleMembershipScenario) {
		s.Owned("ma_owner")
		s.Exec("GRANT ma_owner TO ma_admin WITH ADMIN TRUE, INHERIT FALSE, SET FALSE")
		s.Actor("ma_admin")
		s.Read(false)
		s.Fail("SET ROLE ma_owner", "42501", `permission denied to set role "ma_owner"`, "")
		s.Exec("GRANT ma_owner TO ma_member")
		s.Actor("ma_member")
		message, detail := fixture.RoleMembershipDenied("ma_owner", "grant")
		s.Fail("GRANT ma_owner TO ma_other", "42501", message, detail)
		message, detail = fixture.RoleMembershipDenied("ma_owner", "revoke")
		s.Fail("REVOKE ma_owner FROM ma_other", "42501", message, detail)
		message, detail = fixture.RoleMembershipDenied("ma_member", "grant")
		s.Fail("GRANT ma_member TO ma_other", "42501", message, detail)
		s.Actor("")
		s.Flags("ma_owner", "ma_member", "ma_admin", 1, false, true, true)
		s.Exec("GRANT ma_admin TO ma_actor")
		s.Actor("ma_actor")
		s.Exec("GRANT ma_owner TO ma_other")
		s.Actor("")
		s.Flags("ma_owner", "ma_other", "ma_admin", 1, false, true, true)
		s.Exec("GRANT ma_owner TO ma_actor WITH ADMIN TRUE")
		s.Actor("ma_actor")
		s.Exec("GRANT ma_owner TO ma_other")
		s.Actor("")
		s.Flags("ma_owner", "ma_other", "ma_actor", 1, false, true, true)
		s.Exec("GRANT ma_owner TO ma_other WITH INHERIT FALSE, SET FALSE")
		s.Flags("ma_owner", "ma_other", s.Bootstrap, 1, false, false, false)
		s.Exec("REVOKE ma_owner FROM ma_other GRANTED BY ma_actor")
		s.Actor("ma_other")
		s.Read(true)
		s.Actor("")
		s.Exec("REVOKE ma_owner FROM ma_other GRANTED BY ma_admin")
		s.Actor("ma_other")
		s.Read(false)
		s.Actor("")
		s.Fail("GRANT ma_owner TO ma_other GRANTED BY ma_far", "42501", `permission denied to grant privileges as role "ma_far"`, `The grantor must have the ADMIN option on role "ma_owner".`)
		s.Actor("ma_admin")
		s.Fail("GRANT ma_owner TO ma_other GRANTED BY ma_far", "42501", `permission denied to grant privileges as role "ma_far"`, `Only roles with privileges of role "ma_far" may grant privileges as this role.`)
		s.Fail("REVOKE ma_owner FROM ma_other GRANTED BY ma_far", "42501", `permission denied to revoke privileges granted by role "ma_far"`, `Only roles with privileges of role "ma_far" may revoke privileges granted by this role.`)
	})
}

func TestReferenceRoleMembershipNoninheritAdminCorner(t *testing.T) {
	fixture.RoleMembershipCases(t, []string{"mc_role", "mc_admin", "mc_actor", "mc_member"}, func(s *fixture.RoleMembershipScenario) {
		s.Exec("GRANT mc_role TO mc_admin WITH ADMIN TRUE")
		s.Exec("GRANT mc_admin TO mc_actor WITH INHERIT FALSE")
		s.Actor("mc_actor")
		// Outer MEMBER authority and inferred INHERIT grantor selection differ.
		s.Fail("GRANT mc_role TO mc_member", "XX000", "no possible grantors", "")
		s.Fail("GRANT mc_role TO mc_member GRANTED BY mc_admin", "42501", `permission denied to grant privileges as role "mc_admin"`, `Only roles with privileges of role "mc_admin" may grant privileges as this role.`)
		s.Fail("GRANT mc_role TO mc_member GRANTED BY CURRENT_USER", "42501", `permission denied to grant privileges as role "mc_actor"`, `The grantor must have the ADMIN option on role "mc_role".`)
	})
}

func TestReferenceRoleMembershipNearestGrantor(t *testing.T) {
	fixture.RoleMembershipCases(t, []string{"mn_role", "mn_near", "mn_far", "mn_actor", "mn_member"}, func(s *fixture.RoleMembershipScenario) {
		s.Exec("GRANT mn_role TO mn_near,mn_far WITH ADMIN TRUE")
		s.Exec("GRANT mn_far TO mn_near")
		s.Exec("GRANT mn_near TO mn_actor")
		s.Actor("mn_actor")
		s.Exec("GRANT mn_role TO mn_member")
		s.Actor("")
		s.Flags("mn_role", "mn_member", "mn_near", 1, false, true, true)
		s.Flags("mn_role", "mn_member", "mn_far", 0, false, false, false)
	})
}
