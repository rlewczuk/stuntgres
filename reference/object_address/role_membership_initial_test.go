package reference_test

import (
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoleMembershipCreateInitial(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"mb_new", "mb_overlap", "mb_bad", "mb_user", "mb_group"})
	fixture.RoleMembershipCases(t, []string{"mb_parent", "mb_member", "mb_admin"}, func(s *fixture.RoleMembershipScenario) {
		s.Exec("CREATE ROLE mb_new LOGIN IN ROLE mb_parent ROLE mb_member ADMIN mb_admin")
		s.Flags("mb_parent", "mb_new", s.Bootstrap, 1, false, true, true)
		s.Flags("mb_new", "mb_member", s.Bootstrap, 1, false, true, true)
		s.Flags("mb_new", "mb_admin", s.Bootstrap, 1, true, true, true)
		s.Owned("mb_new")
		s.Actor("mb_member")
		s.Read(true)
		s.Actor("mb_admin")
		s.Exec("GRANT mb_new TO mb_member")
		s.Actor("")
		s.Exec("CREATE ROLE mb_overlap ROLE mb_member ADMIN mb_member")
		s.Flags("mb_overlap", "mb_member", s.Bootstrap, 1, true, true, true)
		s.Exec("CREATE USER mb_user IN GROUP mb_parent USER mb_member ADMIN mb_admin")
		s.Exec("CREATE GROUP mb_group IN ROLE mb_parent ROLE mb_member")
		for _, q := range []string{"CREATE ROLE mb_bad ROLE mb_bad", "CREATE ROLE mb_bad IN ROLE mb_parent ROLE mb_parent"} {
			message := `role "mb_bad" is a member of role "mb_bad"`
			if strings.Contains(q, "IN ROLE") {
				message = `role "mb_bad" is a member of role "mb_parent"`
			}
			s.Fail(q, "0LP01", message, "")
			s.Fail("DROP ROLE mb_bad", "42704", `role "mb_bad" does not exist`, "")
		}
		s.Fail("CREATE ROLE mb_bad IN ROLE mb_parent ROLE mb_member ADMIN mb_absent", "42704", `role "mb_absent" does not exist`, "")
		s.Fail("DROP ROLE mb_bad", "42704", `role "mb_bad" does not exist`, "")
		for _, options := range []string{"IN ROLE mb_parent IN GROUP mb_parent", "ROLE mb_member USER mb_member", "ADMIN mb_admin ADMIN mb_admin"} {
			q := "CREATE ROLE mb_bad " + options
			token := "IN GROUP"
			if strings.HasPrefix(options, "ROLE") {
				token = "USER"
			}
			if strings.HasPrefix(options, "ADMIN") {
				token = "ADMIN"
			}
			step := fixture.RoleReferenceError(q, "42601", "conflicting or redundant options", "", s.Prepared, "execute")
			step.Diagnostic.Position = strconv.Itoa(strings.LastIndex(q, token) + 1)
			s.Run(step)
			s.Fail("DROP ROLE mb_bad", "42704", `role "mb_bad" does not exist`, "")
		}
		s.Exec("CREATE ROLE mb_bad ROLE mb_member")
		s.Exec("DROP ROLE mb_bad,mb_group,mb_user,mb_overlap")
		s.Exec("DROP TABLE membership_owned")
		s.Exec("DROP ROLE mb_new")
	})
}

func TestReferenceRoleMembershipAliases(t *testing.T) {
	fixture.RoleMembershipCases(t, []string{"mg_owner", "mg_admin", "mg_member", "mg_other"}, func(s *fixture.RoleMembershipScenario) {
		s.Owned("mg_owner")
		s.Exec("ALTER GROUP mg_owner ADD USER mg_member")
		s.Flags("mg_owner", "mg_member", s.Bootstrap, 1, false, true, true)
		s.Actor("mg_member")
		s.Read(true)
		s.Fail("ALTER GROUP mg_owner ADD USER mg_other", "42501", "permission denied to alter role", `Only roles with the ADMIN option on role "mg_owner" may add or drop members.`)
		s.Fail("ALTER GROUP mg_owner DROP USER mg_other", "42501", "permission denied to alter role", `Only roles with the ADMIN option on role "mg_owner" may add or drop members.`)
		s.Actor("")
		s.Exec("GRANT mg_owner TO mg_member WITH INHERIT FALSE, SET FALSE")
		s.Exec("ALTER GROUP mg_owner ADD USER mg_member")
		s.Flags("mg_owner", "mg_member", s.Bootstrap, 1, false, false, false)
		s.Exec("ALTER GROUP mg_owner DROP USER mg_member")
		s.Actor("mg_member")
		s.Read(false)
		s.Actor("")
		s.Exec("GRANT mg_owner TO mg_admin WITH ADMIN TRUE")
		s.Actor("mg_admin")
		s.Exec("ALTER GROUP mg_owner ADD USER mg_member")
		s.Actor("")
		s.Restrict("ALTER GROUP mg_owner DROP USER mg_admin")
		s.Exec("REVOKE mg_owner FROM mg_admin CASCADE")
		s.Flags("mg_owner", "mg_member", "mg_admin", 0, false, false, false)
		s.Exec("ALTER GROUP CURRENT_USER ADD USER mg_other")
		s.Exec("ALTER GROUP SESSION_USER DROP USER mg_other")
	})
}
