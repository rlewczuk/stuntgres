package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"

	"github.com/lib/pq"
)

func TestReferenceAccessRoleMembership(t *testing.T) {
	fixture.AccessRoleCases(t, []string{"ar_actor", "ar_middle", "ar_target", "ar_path", "ar_other"}, func(s *fixture.RoleMembershipScenario) {
		fixture.AccessRoleGraph(s)
		fixture.AccessRoleVector(s, "ar_actor", "ar_target", true, false, false, true)
		fixture.AccessRoleVector(s, "ar_actor", "ar_actor", true, true, true, false)
		fixture.AccessRoleVector(s, "ar_other", "ar_target", false, false, false, false)
		fixture.AccessRoleVector(s, s.Bootstrap, "ar_target", true, true, true, true)
		fixture.AccessRoleVector(s, s.Bootstrap, s.Bootstrap, true, true, true, true)
		// Two grant origins on the same member/role pair remain independent.
		s.Exec("GRANT ar_target TO ar_path WITH ADMIN TRUE")
		s.Actor("ar_path")
		s.Exec("GRANT ar_target TO ar_actor WITH INHERIT TRUE, SET TRUE")
		s.Actor("")
		s.Exec("GRANT ar_target TO ar_actor WITH INHERIT FALSE, SET FALSE")
		fixture.AccessRoleVector(s, "ar_actor", "ar_target", true, true, true, true)
		s.Exec("REVOKE ar_target FROM ar_actor GRANTED BY ar_path")
		fixture.AccessRoleVector(s, "ar_actor", "ar_target", true, false, false, true)
		s.Exec("REVOKE ar_target FROM ar_actor")
		s.Exec("ALTER ROLE ar_actor NOINHERIT")
		s.Exec("GRANT ar_middle TO ar_actor WITH INHERIT TRUE")
		fixture.AccessRoleVector(s, "ar_actor", "ar_target", true, true, false, true)
		s.Exec("GRANT ar_target TO ar_middle WITH SET TRUE")
		fixture.AccessRoleVector(s, "ar_actor", "ar_target", true, true, true, true)
		s.Exec("GRANT ar_middle TO ar_actor WITH SET FALSE, INHERIT FALSE")
		fixture.AccessRoleVector(s, "ar_actor", "ar_target", true, false, false, true)
		s.Exec("GRANT ar_target TO ar_path WITH INHERIT TRUE, SET TRUE")
		s.Exec("GRANT ar_path TO ar_actor WITH INHERIT TRUE, SET TRUE")
		fixture.AccessRoleVector(s, "ar_actor", "ar_target", true, true, true, true)
		s.Exec("REVOKE ar_path FROM ar_actor")
		fixture.AccessRoleVector(s, "ar_actor", "ar_target", true, false, false, true)
		s.Exec("GRANT ar_middle TO ar_actor WITH SET TRUE")
		s.Actor("ar_actor")
		s.Exec("SET ROLE ar_middle")
		fixture.AccessRoleBool(s, "SELECT pg_has_role('ar_target','USAGE') AS v", true)
		fixture.AccessRoleBool(s, "SELECT pg_has_role('ar_actor','ar_target','USAGE') AS v", false)
		// Explicit actor cannot borrow the login's role-switch capability.
		fixture.AccessRoleBool(s, "SELECT pg_has_role('ar_other','ar_target','SET') AS v", false)
		s.Exec("RESET ROLE")
		fixture.AccessRoleBool(s, "SELECT pg_has_role('ar_target','USAGE') AS v", false)
	})
}

func TestReferenceAccessRolePrivileges(t *testing.T) {
	fixture.AccessRoleCases(t, []string{"ar_actor", "ar_middle", "ar_target"}, func(s *fixture.RoleMembershipScenario) {
		fixture.AccessRoleGraph(s)
		for _, base := range []string{"MEMBER", "USAGE", "SET"} {
			for _, option := range []string{"ADMIN", "GRANT"} {
				fixture.AccessRoleBool(s, "SELECT pg_has_role('ar_actor','ar_target',$1) AS v", true, base+" WITH "+option+" OPTION")
			}
		}
		for _, token := range []string{" member , SeT ", "MEMBER,MEMBER", "USAGE WITH ADMIN OPTION, SET", "\t\n\v\f\r MEMBER \t\n\v\f\r"} {
			fixture.AccessRoleBool(s, "SELECT pg_has_role('ar_actor','ar_target',$1) AS v", true, token)
		}
		fixture.AccessRoleBool(s, "SELECT pg_has_role('ar_actor','ar_middle','usage,set') AS v", true)
		for _, tc := range []struct{ input, bad string }{
			{"", ""}, {" \t\n", ""}, {",MEMBER", ""}, {"MEMBER,", ""}, {"MEMBER,,SET", ""},
			{"ADMIN", "ADMIN"}, {"SELECT", "SELECT"}, {"MEMBER, bAd ", "bAd"},
			{"MEMBER  WITH ADMIN OPTION", "MEMBER  WITH ADMIN OPTION"},
			{"MEMBER\tWITH ADMIN OPTION", "MEMBER\tWITH ADMIN OPTION"},
			{"\u00a0MEMBER\u00a0", "\u00a0MEMBER\u00a0"},
		} {
			fixture.AccessRoleRuntimeError(s, "SELECT pg_has_role('ar_actor','ar_target',$1)", "22023", fmt.Sprintf("unrecognized privilege type: \"%s\"", tc.bad), tc.input)
		}
	})
}

func TestReferenceAccessRoleNamesAndOIDs(t *testing.T) {
	long := strings.Repeat("x", 61) + "é"
	fixture.AccessRoleCases(t, []string{"ar_actor", "ar_target", "Ar.Mixed Name", long}, func(s *fixture.RoleMembershipScenario) {
		for _, name := range []string{"Ar.Mixed Name", long, long + "extra"} {
			fixture.AccessRoleBool(s, "SELECT pg_has_role($1::name,$1::name,'MEMBER') AS v", true, name)
		}
		for _, name := range []string{"public", "PUBLIC", "ar.missing", `"Ar.Mixed Name"`, "current_user"} {
			fixture.AccessRoleRuntimeError(s, "SELECT pg_has_role($1,'MEMBER')", "42704", fmt.Sprintf("role \"%s\" does not exist", name), name)
			fixture.AccessRoleRuntimeError(s, "SELECT pg_has_role($1,'ar_target','MEMBER')", "42704", fmt.Sprintf("role \"%s\" does not exist", name), name)
		}
		fixture.AccessRoleRuntimeError(s, "SELECT pg_has_role('missing_actor','missing_target','bad')", "42704", `role "missing_actor" does not exist`)
		fixture.AccessRoleRuntimeError(s, "SELECT pg_has_role('ar_actor','missing_target','bad')", "42704", `role "missing_target" does not exist`)
		actor := fixture.AccessRoleOID(s, "ar_actor")
		super := fixture.AccessRoleOID(s, s.Bootstrap)
		for _, missing := range []int64{0, 4294967294, 4294967295} {
			var count int
			if err := s.C.QueryRowContext(s.P.Ctx, "SELECT count(*) FROM pg_roles WHERE oid=$1", missing).Scan(&count); err != nil || count != 0 {
				s.T.Fatal("missing role precondition", count, err)
			}
			for _, tc := range []struct {
				a, b          int64
				member, admin bool
			}{
				{missing, missing, true, false}, {missing, actor, false, false}, {actor, missing, false, false}, {super, missing, true, true},
			} {
				for _, privilege := range []string{"MEMBER", "USAGE", "SET"} {
					fixture.AccessRoleBool(s, "SELECT pg_has_role($1::oid,$2::oid,$3) AS v", tc.member, tc.a, tc.b, privilege)
				}
				fixture.AccessRoleBool(s, "SELECT pg_has_role($1::oid,$2::oid,'SET WITH ADMIN OPTION') AS v", tc.admin, tc.a, tc.b)
			}
		}
		fixture.AccessRoleBool(s, "SELECT pg_has_role(4294967294::oid,4294967295::oid,'MEMBER') AS v", false)
		fixture.AccessRoleRuntimeError(s, "SELECT pg_has_role(0::oid,0::oid,'bad')", "22023", `unrecognized privilege type: "bad"`)
		s.Exec("ALTER ROLE ar_target RENAME TO ar_renamed")
		fixture.AccessRoleBool(s, "SELECT pg_has_role('ar_renamed','ar_renamed','MEMBER') AS v", true)
		s.Exec("ALTER ROLE ar_renamed RENAME TO ar_target")
		s.Exec("DROP ROLE " + pq.QuoteIdentifier(long))
	})
}
