package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoleMembershipDefaultsOptions(t *testing.T) {
	fixture.RoleMembershipCases(t, []string{"mo_owner", "mo_member", "mo_other"}, func(s *fixture.RoleMembershipScenario) {
		t := s.T
		s.Owned("mo_owner")
		s.Exec("GRANT mo_owner TO mo_member")
		s.Flags("mo_owner", "mo_member", s.Bootstrap, 1, false, true, true)
		s.Actor("mo_member")
		s.Read(true)
		s.Exec("SET ROLE mo_owner")
		s.Exec("RESET ROLE")
		s.Actor("")
		s.Exec("GRANT mo_owner TO mo_member WITH ADMIN TRUE, INHERIT FALSE, SET FALSE")
		s.Flags("mo_owner", "mo_member", s.Bootstrap, 1, true, false, false)
		s.Actor("mo_member")
		s.Read(false)
		s.Fail("SET ROLE mo_owner", "42501", `permission denied to set role "mo_owner"`, "")
		s.Exec("GRANT mo_owner TO mo_other")
		s.Actor("")
		s.Exec("GRANT mo_owner TO mo_member WITH INHERIT OPTION")
		s.Flags("mo_owner", "mo_member", s.Bootstrap, 1, true, true, false)
		s.ClearNotices()
		s.Exec("GRANT mo_owner TO mo_member")
		s.Notice("NOTICE", "00000", fmt.Sprintf(`role "mo_member" has already been granted membership in role "mo_owner" by role "%s"`, s.Bootstrap))
		s.Flags("mo_owner", "mo_member", s.Bootstrap, 1, true, true, false)
		s.Exec("GRANT mo_owner TO mo_member WITH INHERIT FALSE, INHERIT TRUE, SET TRUE, SET FALSE, ADMIN FALSE, ADMIN TRUE")
		s.Flags("mo_owner", "mo_member", s.Bootstrap, 1, true, true, false)
		s.Actor("mo_member")
		s.Read(true)
		s.Actor("")
		for _, option := range []string{"INHERIT", "SET"} {
			s.Exec("REVOKE " + option + " OPTION FOR mo_owner FROM mo_member")
			s.Flags("mo_owner", "mo_member", s.Bootstrap, 1, true, false, false)
			s.Flags("mo_owner", "mo_other", "mo_member", 1, false, true, true)
		}
		s.Exec("GRANT mo_owner TO mo_member WITH ADMIN FALSE")
		// GRANT's update is deliberately not REVOKE dependency planning (REL_18_0 user.c).
		s.Flags("mo_owner", "mo_other", "mo_member", 1, false, true, true)
		s.ClearNotices()
		s.Exec("REVOKE ADMIN OPTION FOR mo_owner FROM mo_member")
		if len(*s.Notices) != 0 {
			t.Fatal("already-false ADMIN warned", *s.Notices)
		}
		s.Exec("REVOKE mo_owner FROM mo_other GRANTED BY mo_member CASCADE")
		s.ClearNotices()
		s.Exec("REVOKE mo_owner FROM mo_other")
		s.Notice("WARNING", "01000", fmt.Sprintf(`role "mo_other" has not been granted membership in role "mo_owner" by role "%s"`, s.Bootstrap))
	})
}

func TestReferenceRoleMembershipInheritance(t *testing.T) {
	fixture.RoleMembershipCases(t, []string{"mi_owner", "mi_middle", "mi_member", "mi_path"}, func(s *fixture.RoleMembershipScenario) {
		t := s.T
		s.Owned("mi_owner")
		s.Exec("GRANT mi_owner TO mi_middle WITH INHERIT FALSE")
		s.Exec("GRANT mi_middle TO mi_member")
		s.Actor("mi_member")
		s.Read(false)
		s.Actor("")
		s.Exec("GRANT mi_owner TO mi_path")
		s.Exec("GRANT mi_path TO mi_member")
		s.Actor("mi_member")
		s.Read(true)
		stmt, err := s.C.PrepareContext(s.P.Ctx, "SELECT id FROM membership_owned ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		check := func(allow bool) {
			rows, err := stmt.QueryContext(s.P.Ctx)
			if !allow {
				want := fixture.ControlsReferenceDiagnostic("42501", "permission denied for table membership_owned")
				fixture.AssertDiagnostic(t, err, &want)
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
		s.Actor("")
		s.Exec("REVOKE INHERIT OPTION FOR mi_path FROM mi_member")
		s.Actor("mi_member")
		check(false)
		s.Actor("")
		s.Exec("GRANT mi_owner TO mi_middle WITH INHERIT TRUE")
		s.Actor("mi_member")
		check(true)
	})
}
