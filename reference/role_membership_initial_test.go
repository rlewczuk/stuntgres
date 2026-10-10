package reference_test

import (
	"strconv"
	"strings"
	"testing"
)

func TestReferenceRoleMembershipCreateInitial(t *testing.T) {
	roleReferenceCleanup(t, []string{"mb_new", "mb_overlap", "mb_bad", "mb_user", "mb_group"})
	membershipCases(t, []string{"mb_parent", "mb_member", "mb_admin"}, func(s *membershipScenario) {
		s.exec("CREATE ROLE mb_new LOGIN IN ROLE mb_parent ROLE mb_member ADMIN mb_admin")
		s.flags("mb_parent", "mb_new", s.bootstrap, 1, false, true, true)
		s.flags("mb_new", "mb_member", s.bootstrap, 1, false, true, true)
		s.flags("mb_new", "mb_admin", s.bootstrap, 1, true, true, true)
		s.owned("mb_new")
		s.actor("mb_member")
		s.read(true)
		s.actor("mb_admin")
		s.exec("GRANT mb_new TO mb_member")
		s.actor("")
		s.exec("CREATE ROLE mb_overlap ROLE mb_member ADMIN mb_member")
		s.flags("mb_overlap", "mb_member", s.bootstrap, 1, true, true, true)
		s.exec("CREATE USER mb_user IN GROUP mb_parent USER mb_member ADMIN mb_admin")
		s.exec("CREATE GROUP mb_group IN ROLE mb_parent ROLE mb_member")
		for _, q := range []string{"CREATE ROLE mb_bad ROLE mb_bad", "CREATE ROLE mb_bad IN ROLE mb_parent ROLE mb_parent"} {
			message := `role "mb_bad" is a member of role "mb_bad"`
			if strings.Contains(q, "IN ROLE") {
				message = `role "mb_bad" is a member of role "mb_parent"`
			}
			s.fail(q, "0LP01", message, "")
			s.fail("DROP ROLE mb_bad", "42704", `role "mb_bad" does not exist`, "")
		}
		s.fail("CREATE ROLE mb_bad IN ROLE mb_parent ROLE mb_member ADMIN mb_absent", "42704", `role "mb_absent" does not exist`, "")
		s.fail("DROP ROLE mb_bad", "42704", `role "mb_bad" does not exist`, "")
		for _, options := range []string{"IN ROLE mb_parent IN GROUP mb_parent", "ROLE mb_member USER mb_member", "ADMIN mb_admin ADMIN mb_admin"} {
			q := "CREATE ROLE mb_bad " + options
			token := "IN GROUP"
			if strings.HasPrefix(options, "ROLE") {
				token = "USER"
			}
			if strings.HasPrefix(options, "ADMIN") {
				token = "ADMIN"
			}
			step := roleReferenceError(q, "42601", "conflicting or redundant options", "", s.prepared, "execute")
			step.diagnostic.Position = strconv.Itoa(strings.LastIndex(q, token) + 1)
			s.run(step)
			s.fail("DROP ROLE mb_bad", "42704", `role "mb_bad" does not exist`, "")
		}
		s.exec("CREATE ROLE mb_bad ROLE mb_member")
		s.exec("DROP ROLE mb_bad,mb_group,mb_user,mb_overlap")
		s.exec("DROP TABLE membership_owned")
		s.exec("DROP ROLE mb_new")
	})
}

func TestReferenceRoleMembershipAliases(t *testing.T) {
	membershipCases(t, []string{"mg_owner", "mg_admin", "mg_member", "mg_other"}, func(s *membershipScenario) {
		s.owned("mg_owner")
		s.exec("ALTER GROUP mg_owner ADD USER mg_member")
		s.flags("mg_owner", "mg_member", s.bootstrap, 1, false, true, true)
		s.actor("mg_member")
		s.read(true)
		s.fail("ALTER GROUP mg_owner ADD USER mg_other", "42501", "permission denied to alter role", `Only roles with the ADMIN option on role "mg_owner" may add or drop members.`)
		s.fail("ALTER GROUP mg_owner DROP USER mg_other", "42501", "permission denied to alter role", `Only roles with the ADMIN option on role "mg_owner" may add or drop members.`)
		s.actor("")
		s.exec("GRANT mg_owner TO mg_member WITH INHERIT FALSE, SET FALSE")
		s.exec("ALTER GROUP mg_owner ADD USER mg_member")
		s.flags("mg_owner", "mg_member", s.bootstrap, 1, false, false, false)
		s.exec("ALTER GROUP mg_owner DROP USER mg_member")
		s.actor("mg_member")
		s.read(false)
		s.actor("")
		s.exec("GRANT mg_owner TO mg_admin WITH ADMIN TRUE")
		s.actor("mg_admin")
		s.exec("ALTER GROUP mg_owner ADD USER mg_member")
		s.actor("")
		s.restrict("ALTER GROUP mg_owner DROP USER mg_admin")
		s.exec("REVOKE mg_owner FROM mg_admin CASCADE")
		s.flags("mg_owner", "mg_member", "mg_admin", 0, false, false, false)
		s.exec("ALTER GROUP CURRENT_USER ADD USER mg_other")
		s.exec("ALTER GROUP SESSION_USER DROP USER mg_other")
	})
}
