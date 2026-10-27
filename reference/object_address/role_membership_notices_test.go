package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoleMembershipNoticesAtomicity(t *testing.T) {
	fixture.RoleMembershipCases(t, []string{"mw_role", "mw_member", "mw_other"}, func(s *fixture.RoleMembershipScenario) {
		t := s.T
		s.Exec("GRANT mw_role TO mw_member")
		repeated := fmt.Sprintf(`role "mw_member" has already been granted membership in role "mw_role" by role "%s"`, s.Bootstrap)
		absent := fmt.Sprintf(`role "mw_other" has not been granted membership in role "mw_role" by role "%s"`, s.Bootstrap)
		s.ClearNotices()
		s.Exec("GRANT mw_role TO mw_member,mw_member")
		want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: repeated}
		if len(*s.Notices) != 2 || (*s.Notices)[0] != want || (*s.Notices)[1] != want {
			t.Fatal("duplicate member notices", *s.Notices)
		}
		s.ClearNotices()
		s.Exec("REVOKE mw_role FROM mw_member,mw_member")
		if len(*s.Notices) != 0 {
			t.Fatal("duplicate revoke snapshot warned", *s.Notices)
		}
		s.Flags("mw_role", "mw_member", s.Bootstrap, 0, false, false, false)
		s.Exec("GRANT mw_role TO mw_member")
		s.ClearNotices()
		// Notice emission precedes later target lookup; statement changes still roll back.
		s.Fail("GRANT mw_role,mw_absent TO mw_member", "42704", `role "mw_absent" does not exist`, "")
		s.Notice("NOTICE", "00000", repeated)
		s.ClearNotices()
		s.Fail("REVOKE mw_role,mw_absent FROM mw_other", "42704", `role "mw_absent" does not exist`, "")
		s.Notice("WARNING", "01000", absent)
		s.Flags("mw_role", "mw_member", s.Bootstrap, 1, false, true, true)
		s.Exec("SET client_min_messages TO warning")
		s.ClearNotices()
		s.Exec("GRANT mw_role TO mw_member")
		if len(*s.Notices) != 0 {
			t.Fatal("NOTICE filtering", *s.Notices)
		}
		s.Exec("REVOKE mw_role FROM mw_other")
		s.Notice("WARNING", "01000", absent)
		s.Exec("RESET client_min_messages")
		stmt, err := s.C.PrepareContext(s.P.Ctx, "GRANT mw_role TO mw_member")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		if len(*s.Notices) != 0 {
			t.Fatal("Prepare emitted notice", *s.Notices)
		}
		result, err := stmt.ExecContext(s.P.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		n, err := result.RowsAffected()
		if n != 0 || err != nil {
			t.Fatal(n, err)
		}
		s.Notice("NOTICE", "00000", repeated)
	})
}

func TestReferenceRoleMembershipSuperuserTarget(t *testing.T) {
	fixture.RoleMembershipCases(t, []string{"mu_admin", "mu_member"}, func(s *fixture.RoleMembershipScenario) {
		s.Exec("GRANT " + s.Bootstrap + " TO mu_admin WITH ADMIN TRUE, INHERIT FALSE, SET FALSE")
		s.Actor("mu_admin")
		s.Fail("GRANT "+s.Bootstrap+" TO mu_member", "42501", "permission denied to grant role \""+s.Bootstrap+"\"", `Only roles with the SUPERUSER attribute may grant roles with the SUPERUSER attribute.`)
		s.Fail("REVOKE "+s.Bootstrap+" FROM mu_member", "42501", "permission denied to revoke role \""+s.Bootstrap+"\"", `Only roles with the SUPERUSER attribute may revoke roles with the SUPERUSER attribute.`)
		s.Fail("ALTER GROUP "+s.Bootstrap+" ADD USER mu_member", "42501", "permission denied to alter role", `Only roles with the SUPERUSER attribute may alter roles with the SUPERUSER attribute.`)
	})
}
