package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceRoleMembershipDefaultsOptions(t *testing.T) {
	membershipCases(t, []string{"mo_owner", "mo_member", "mo_other"}, func(s *membershipScenario) {
		t := s.t
		s.owned("mo_owner")
		s.exec("GRANT mo_owner TO mo_member")
		s.flags("mo_owner", "mo_member", s.bootstrap, 1, false, true, true)
		s.actor("mo_member")
		s.read(true)
		s.exec("SET ROLE mo_owner")
		s.exec("RESET ROLE")
		s.actor("")
		s.exec("GRANT mo_owner TO mo_member WITH ADMIN TRUE, INHERIT FALSE, SET FALSE")
		s.flags("mo_owner", "mo_member", s.bootstrap, 1, true, false, false)
		s.actor("mo_member")
		s.read(false)
		s.fail("SET ROLE mo_owner", "42501", `permission denied to set role "mo_owner"`, "")
		s.exec("GRANT mo_owner TO mo_other")
		s.actor("")
		s.exec("GRANT mo_owner TO mo_member WITH INHERIT OPTION")
		s.flags("mo_owner", "mo_member", s.bootstrap, 1, true, true, false)
		s.clearNotices()
		s.exec("GRANT mo_owner TO mo_member")
		s.notice("NOTICE", "00000", fmt.Sprintf(`role "mo_member" has already been granted membership in role "mo_owner" by role "%s"`, s.bootstrap))
		s.flags("mo_owner", "mo_member", s.bootstrap, 1, true, true, false)
		s.exec("GRANT mo_owner TO mo_member WITH INHERIT FALSE, INHERIT TRUE, SET TRUE, SET FALSE, ADMIN FALSE, ADMIN TRUE")
		s.flags("mo_owner", "mo_member", s.bootstrap, 1, true, true, false)
		s.actor("mo_member")
		s.read(true)
		s.actor("")
		for _, option := range []string{"INHERIT", "SET"} {
			s.exec("REVOKE " + option + " OPTION FOR mo_owner FROM mo_member")
			s.flags("mo_owner", "mo_member", s.bootstrap, 1, true, false, false)
			s.flags("mo_owner", "mo_other", "mo_member", 1, false, true, true)
		}
		s.exec("GRANT mo_owner TO mo_member WITH ADMIN FALSE")
		// GRANT's update is deliberately not REVOKE dependency planning (REL_18_0 user.c).
		s.flags("mo_owner", "mo_other", "mo_member", 1, false, true, true)
		s.clearNotices()
		s.exec("REVOKE ADMIN OPTION FOR mo_owner FROM mo_member")
		if len(*s.notices) != 0 {
			t.Fatal("already-false ADMIN warned", *s.notices)
		}
		s.exec("REVOKE mo_owner FROM mo_other GRANTED BY mo_member CASCADE")
		s.clearNotices()
		s.exec("REVOKE mo_owner FROM mo_other")
		s.notice("WARNING", "01000", fmt.Sprintf(`role "mo_other" has not been granted membership in role "mo_owner" by role "%s"`, s.bootstrap))
	})
}

func TestReferenceRoleMembershipInheritance(t *testing.T) {
	membershipCases(t, []string{"mi_owner", "mi_middle", "mi_member", "mi_path"}, func(s *membershipScenario) {
		t := s.t
		s.owned("mi_owner")
		s.exec("GRANT mi_owner TO mi_middle WITH INHERIT FALSE")
		s.exec("GRANT mi_middle TO mi_member")
		s.actor("mi_member")
		s.read(false)
		s.actor("")
		s.exec("GRANT mi_owner TO mi_path")
		s.exec("GRANT mi_path TO mi_member")
		s.actor("mi_member")
		s.read(true)
		stmt, err := s.c.PrepareContext(s.p.ctx, "SELECT id FROM membership_owned ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		check := func(allow bool) {
			rows, err := stmt.QueryContext(s.p.ctx)
			if !allow {
				want := controlsReferenceDiagnostic("42501", "permission denied for table membership_owned")
				referenceAssertDiagnostic(t, err, &want)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			columns, err := rows.Columns()
			if err != nil || len(columns) != 1 || columns[0] != "id" {
				t.Fatal(columns, err)
			}
			types, err := rows.ColumnTypes()
			if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "INT4" {
				t.Fatal(types, err)
			}
			var id int
			if !rows.Next() || rows.Scan(&id) != nil || id != 7 || rows.Next() || rows.Err() != nil {
				t.Fatal("held prepared rows", id, rows.Err())
			}
		}
		check(true)
		s.actor("")
		s.exec("REVOKE INHERIT OPTION FOR mi_path FROM mi_member")
		s.actor("mi_member")
		check(false)
		s.actor("")
		s.exec("GRANT mi_owner TO mi_middle WITH INHERIT TRUE")
		s.actor("mi_member")
		check(true)
	})
}
