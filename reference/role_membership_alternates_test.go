package reference_test

import "testing"

func TestReferenceRoleMembershipAlternateAdminSources(t *testing.T) {
	membershipCases(t, []string{"mz_role", "mz_a", "mz_b", "mz_actor", "mz_member"}, func(s *membershipScenario) {
		t := s.t
		s.owned("mz_role")
		s.exec("GRANT mz_role TO mz_a,mz_b WITH ADMIN TRUE")
		s.exec("GRANT mz_a,mz_b TO mz_actor")
		s.actor("mz_actor")
		s.exec("GRANT mz_role TO mz_member")
		s.actor("")
		if s.backend.pg {
			var grantor string
			err := s.c.QueryRowContext(s.p.ctx, `SELECT g.rolname FROM pg_auth_members a JOIN pg_roles r ON r.oid=a.roleid JOIN pg_roles m ON m.oid=a.member JOIN pg_roles g ON g.oid=a.grantor WHERE r.rolname='mz_role' AND m.rolname='mz_member'`).Scan(&grantor)
			if err != nil || (grantor != "mz_a" && grantor != "mz_b") {
				t.Fatal("nearest tie must select an eligible direct admin", grantor, err)
			}
		}
		s.actor("mz_member")
		s.read(true)
		s.actor("")
		s.exec("REVOKE ADMIN OPTION FOR mz_role FROM mz_a CASCADE")
		s.exec("REVOKE ADMIN OPTION FOR mz_role FROM mz_b CASCADE")
		s.actor("mz_member")
		s.read(false)
		s.actor("")
	})
}

func TestReferenceRoleMembershipAdminFalseGrantorCleanup(t *testing.T) {
	membershipCases(t, []string{"mf_role", "mf_admin", "mf_member"}, func(s *membershipScenario) {
		s.owned("mf_role")
		s.exec("GRANT mf_role TO mf_admin WITH ADMIN TRUE")
		s.actor("mf_admin")
		s.exec("GRANT mf_role TO mf_member")
		s.actor("")
		s.exec("GRANT mf_role TO mf_admin WITH ADMIN FALSE")
		s.actor("mf_member")
		s.read(true)
		s.actor("")
		s.fail("GRANT mf_role TO mf_member GRANTED BY mf_admin", "42501", `permission denied to grant privileges as role "mf_admin"`, `The grantor must have the ADMIN option on role "mf_role".`)
		// Explicit REVOKE does not require the named grantor's continuing ADMIN.
		s.exec("REVOKE mf_role FROM mf_member GRANTED BY mf_admin")
		s.actor("mf_member")
		s.read(false)
		s.actor("")
	})
}
