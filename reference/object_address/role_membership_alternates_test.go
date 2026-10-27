package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoleMembershipAlternateAdminSources(t *testing.T) {
	fixture.RoleMembershipCases(t, []string{"mz_role", "mz_a", "mz_b", "mz_actor", "mz_member"}, func(s *fixture.RoleMembershipScenario) {
		t := s.T
		s.Owned("mz_role")
		s.Exec("GRANT mz_role TO mz_a,mz_b WITH ADMIN TRUE")
		s.Exec("GRANT mz_a,mz_b TO mz_actor")
		s.Actor("mz_actor")
		s.Exec("GRANT mz_role TO mz_member")
		s.Actor("")
		if s.Backend.PG {
			var grantor string
			err := s.C.QueryRowContext(s.P.Ctx, `SELECT g.rolname FROM pg_auth_members a JOIN pg_roles r ON r.oid=a.roleid JOIN pg_roles m ON m.oid=a.member JOIN pg_roles g ON g.oid=a.grantor WHERE r.rolname='mz_role' AND m.rolname='mz_member'`).Scan(&grantor)
			if err != nil || (grantor != "mz_a" && grantor != "mz_b") {
				t.Fatal("nearest tie must select an eligible direct admin", grantor, err)
			}
		}
		s.Actor("mz_member")
		s.Read(true)
		s.Actor("")
		s.Exec("REVOKE ADMIN OPTION FOR mz_role FROM mz_a CASCADE")
		s.Exec("REVOKE ADMIN OPTION FOR mz_role FROM mz_b CASCADE")
		s.Actor("mz_member")
		s.Read(false)
		s.Actor("")
	})
}

func TestReferenceRoleMembershipAdminFalseGrantorCleanup(t *testing.T) {
	fixture.RoleMembershipCases(t, []string{"mf_role", "mf_admin", "mf_member"}, func(s *fixture.RoleMembershipScenario) {
		s.Owned("mf_role")
		s.Exec("GRANT mf_role TO mf_admin WITH ADMIN TRUE")
		s.Actor("mf_admin")
		s.Exec("GRANT mf_role TO mf_member")
		s.Actor("")
		s.Exec("GRANT mf_role TO mf_admin WITH ADMIN FALSE")
		s.Actor("mf_member")
		s.Read(true)
		s.Actor("")
		s.Fail("GRANT mf_role TO mf_member GRANTED BY mf_admin", "42501", `permission denied to grant privileges as role "mf_admin"`, `The grantor must have the ADMIN option on role "mf_role".`)
		// Explicit REVOKE does not require the named grantor's continuing ADMIN.
		s.Exec("REVOKE mf_role FROM mf_member GRANTED BY mf_admin")
		s.Actor("mf_member")
		s.Read(false)
		s.Actor("")
	})
}
