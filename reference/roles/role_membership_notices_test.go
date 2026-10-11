package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoleMembershipNoticesAtomicity(t *testing.T) {
	membershipCases(t, []string{"mw_role", "mw_member", "mw_other"}, func(s *membershipScenario) {
		t := s.t
		s.exec("GRANT mw_role TO mw_member")
		repeated := fmt.Sprintf(`role "mw_member" has already been granted membership in role "mw_role" by role "%s"`, s.bootstrap)
		absent := fmt.Sprintf(`role "mw_other" has not been granted membership in role "mw_role" by role "%s"`, s.bootstrap)
		s.clearNotices()
		s.exec("GRANT mw_role TO mw_member,mw_member")
		want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: repeated}
		if len(*s.notices) != 2 || (*s.notices)[0] != want || (*s.notices)[1] != want {
			t.Fatal("duplicate member notices", *s.notices)
		}
		s.clearNotices()
		s.exec("REVOKE mw_role FROM mw_member,mw_member")
		if len(*s.notices) != 0 {
			t.Fatal("duplicate revoke snapshot warned", *s.notices)
		}
		s.flags("mw_role", "mw_member", s.bootstrap, 0, false, false, false)
		s.exec("GRANT mw_role TO mw_member")
		s.clearNotices()
		// Notice emission precedes later target lookup; statement changes still roll back.
		s.fail("GRANT mw_role,mw_absent TO mw_member", "42704", `role "mw_absent" does not exist`, "")
		s.notice("NOTICE", "00000", repeated)
		s.clearNotices()
		s.fail("REVOKE mw_role,mw_absent FROM mw_other", "42704", `role "mw_absent" does not exist`, "")
		s.notice("WARNING", "01000", absent)
		s.flags("mw_role", "mw_member", s.bootstrap, 1, false, true, true)
		s.exec("SET client_min_messages TO warning")
		s.clearNotices()
		s.exec("GRANT mw_role TO mw_member")
		if len(*s.notices) != 0 {
			t.Fatal("NOTICE filtering", *s.notices)
		}
		s.exec("REVOKE mw_role FROM mw_other")
		s.notice("WARNING", "01000", absent)
		s.exec("RESET client_min_messages")
		stmt, err := s.c.PrepareContext(s.p.Ctx, "GRANT mw_role TO mw_member")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		if len(*s.notices) != 0 {
			t.Fatal("Prepare emitted notice", *s.notices)
		}
		result, err := stmt.ExecContext(s.p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		n, err := result.RowsAffected()
		if n != 0 || err != nil {
			t.Fatal(n, err)
		}
		s.notice("NOTICE", "00000", repeated)
	})
}

func TestReferenceRoleMembershipSuperuserTarget(t *testing.T) {
	membershipCases(t, []string{"mu_admin", "mu_member"}, func(s *membershipScenario) {
		s.exec("GRANT " + s.bootstrap + " TO mu_admin WITH ADMIN TRUE, INHERIT FALSE, SET FALSE")
		s.actor("mu_admin")
		s.fail("GRANT "+s.bootstrap+" TO mu_member", "42501", "permission denied to grant role \""+s.bootstrap+"\"", `Only roles with the SUPERUSER attribute may grant roles with the SUPERUSER attribute.`)
		s.fail("REVOKE "+s.bootstrap+" FROM mu_member", "42501", "permission denied to revoke role \""+s.bootstrap+"\"", `Only roles with the SUPERUSER attribute may revoke roles with the SUPERUSER attribute.`)
		s.fail("ALTER GROUP "+s.bootstrap+" ADD USER mu_member", "42501", "permission denied to alter role", `Only roles with the SUPERUSER attribute may alter roles with the SUPERUSER attribute.`)
	})
}
