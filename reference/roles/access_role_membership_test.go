package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/lib/pq"
)

func TestReferenceAccessRoleMembership(t *testing.T) {
	accessRoleCases(t, []string{"ar_actor", "ar_middle", "ar_target", "ar_path", "ar_other"}, func(s *membershipScenario) {
		accessRoleGraph(s)
		accessRoleVector(s, "ar_actor", "ar_target", true, false, false, true)
		accessRoleVector(s, "ar_actor", "ar_actor", true, true, true, false)
		accessRoleVector(s, "ar_other", "ar_target", false, false, false, false)
		accessRoleVector(s, s.bootstrap, "ar_target", true, true, true, true)
		accessRoleVector(s, s.bootstrap, s.bootstrap, true, true, true, true)
		// Two grant origins on the same member/role pair remain independent.
		s.exec("GRANT ar_target TO ar_path WITH ADMIN TRUE")
		s.actor("ar_path")
		s.exec("GRANT ar_target TO ar_actor WITH INHERIT TRUE, SET TRUE")
		s.actor("")
		s.exec("GRANT ar_target TO ar_actor WITH INHERIT FALSE, SET FALSE")
		accessRoleVector(s, "ar_actor", "ar_target", true, true, true, true)
		s.exec("REVOKE ar_target FROM ar_actor GRANTED BY ar_path")
		accessRoleVector(s, "ar_actor", "ar_target", true, false, false, true)
		s.exec("REVOKE ar_target FROM ar_actor")
		s.exec("ALTER ROLE ar_actor NOINHERIT")
		s.exec("GRANT ar_middle TO ar_actor WITH INHERIT TRUE")
		accessRoleVector(s, "ar_actor", "ar_target", true, true, false, true)
		s.exec("GRANT ar_target TO ar_middle WITH SET TRUE")
		accessRoleVector(s, "ar_actor", "ar_target", true, true, true, true)
		s.exec("GRANT ar_middle TO ar_actor WITH SET FALSE, INHERIT FALSE")
		accessRoleVector(s, "ar_actor", "ar_target", true, false, false, true)
		s.exec("GRANT ar_target TO ar_path WITH INHERIT TRUE, SET TRUE")
		s.exec("GRANT ar_path TO ar_actor WITH INHERIT TRUE, SET TRUE")
		accessRoleVector(s, "ar_actor", "ar_target", true, true, true, true)
		s.exec("REVOKE ar_path FROM ar_actor")
		accessRoleVector(s, "ar_actor", "ar_target", true, false, false, true)
		s.exec("GRANT ar_middle TO ar_actor WITH SET TRUE")
		s.actor("ar_actor")
		s.exec("SET ROLE ar_middle")
		accessRoleBool(s, "SELECT pg_has_role('ar_target','USAGE') AS v", true)
		accessRoleBool(s, "SELECT pg_has_role('ar_actor','ar_target','USAGE') AS v", false)
		// Explicit actor cannot borrow the login's role-switch capability.
		accessRoleBool(s, "SELECT pg_has_role('ar_other','ar_target','SET') AS v", false)
		s.exec("RESET ROLE")
		accessRoleBool(s, "SELECT pg_has_role('ar_target','USAGE') AS v", false)
	})
}

func TestReferenceAccessRolePrivileges(t *testing.T) {
	accessRoleCases(t, []string{"ar_actor", "ar_middle", "ar_target"}, func(s *membershipScenario) {
		accessRoleGraph(s)
		for _, base := range []string{"MEMBER", "USAGE", "SET"} {
			for _, option := range []string{"ADMIN", "GRANT"} {
				accessRoleBool(s, "SELECT pg_has_role('ar_actor','ar_target',$1) AS v", true, base+" WITH "+option+" OPTION")
			}
		}
		for _, token := range []string{" member , SeT ", "MEMBER,MEMBER", "USAGE WITH ADMIN OPTION, SET", "\t\n\v\f\r MEMBER \t\n\v\f\r"} {
			accessRoleBool(s, "SELECT pg_has_role('ar_actor','ar_target',$1) AS v", true, token)
		}
		accessRoleBool(s, "SELECT pg_has_role('ar_actor','ar_middle','usage,set') AS v", true)
		for _, tc := range []struct{ input, bad string }{
			{"", ""}, {" \t\n", ""}, {",MEMBER", ""}, {"MEMBER,", ""}, {"MEMBER,,SET", ""},
			{"ADMIN", "ADMIN"}, {"SELECT", "SELECT"}, {"MEMBER, bAd ", "bAd"},
			{"MEMBER  WITH ADMIN OPTION", "MEMBER  WITH ADMIN OPTION"},
			{"MEMBER\tWITH ADMIN OPTION", "MEMBER\tWITH ADMIN OPTION"},
			{"\u00a0MEMBER\u00a0", "\u00a0MEMBER\u00a0"},
		} {
			accessRoleRuntimeError(s, "SELECT pg_has_role('ar_actor','ar_target',$1)", "22023", fmt.Sprintf("unrecognized privilege type: \"%s\"", tc.bad), tc.input)
		}
	})
}

func TestReferenceAccessRoleNamesAndOIDs(t *testing.T) {
	long := strings.Repeat("x", 61) + "é"
	accessRoleCases(t, []string{"ar_actor", "ar_target", "Ar.Mixed Name", long}, func(s *membershipScenario) {
		for _, name := range []string{"Ar.Mixed Name", long, long + "extra"} {
			accessRoleBool(s, "SELECT pg_has_role($1::name,$1::name,'MEMBER') AS v", true, name)
		}
		for _, name := range []string{"public", "PUBLIC", "ar.missing", `"Ar.Mixed Name"`, "current_user"} {
			accessRoleRuntimeError(s, "SELECT pg_has_role($1,'MEMBER')", "42704", fmt.Sprintf("role \"%s\" does not exist", name), name)
			accessRoleRuntimeError(s, "SELECT pg_has_role($1,'ar_target','MEMBER')", "42704", fmt.Sprintf("role \"%s\" does not exist", name), name)
		}
		accessRoleRuntimeError(s, "SELECT pg_has_role('missing_actor','missing_target','bad')", "42704", `role "missing_actor" does not exist`)
		accessRoleRuntimeError(s, "SELECT pg_has_role('ar_actor','missing_target','bad')", "42704", `role "missing_target" does not exist`)
		actor := accessRoleOID(s, "ar_actor")
		super := accessRoleOID(s, s.bootstrap)
		for _, missing := range []int64{0, 4294967294, 4294967295} {
			var count int
			if err := s.c.QueryRowContext(s.p.Ctx, "SELECT count(*) FROM pg_roles WHERE oid=$1", missing).Scan(&count); err != nil || count != 0 {
				s.t.Fatal("missing role precondition", count, err)
			}
			for _, tc := range []struct {
				a, b          int64
				member, admin bool
			}{
				{missing, missing, true, false}, {missing, actor, false, false}, {actor, missing, false, false}, {super, missing, true, true},
			} {
				for _, privilege := range []string{"MEMBER", "USAGE", "SET"} {
					accessRoleBool(s, "SELECT pg_has_role($1::oid,$2::oid,$3) AS v", tc.member, tc.a, tc.b, privilege)
				}
				accessRoleBool(s, "SELECT pg_has_role($1::oid,$2::oid,'SET WITH ADMIN OPTION') AS v", tc.admin, tc.a, tc.b)
			}
		}
		accessRoleBool(s, "SELECT pg_has_role(4294967294::oid,4294967295::oid,'MEMBER') AS v", false)
		accessRoleRuntimeError(s, "SELECT pg_has_role(0::oid,0::oid,'bad')", "22023", `unrecognized privilege type: "bad"`)
		// Rename remains a supplemental PostgreSQL-only DDL witness.
		if s.backend.PG {
			s.exec("ALTER ROLE ar_target RENAME TO ar_renamed")
			accessRoleBool(s, "SELECT pg_has_role('ar_renamed','ar_renamed','MEMBER') AS v", true)
			s.exec("ALTER ROLE ar_renamed RENAME TO ar_target")
		}
		s.exec("DROP ROLE " + pq.QuoteIdentifier(long))
	})
}
