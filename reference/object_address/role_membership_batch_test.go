package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoleMembershipBatchCandidateFreshness(t *testing.T) {
	fixture.RoleMembershipCases(t, []string{"mf_a", "mf_b", "mf_c"}, func(s *fixture.RoleMembershipScenario) {
		t := s.T
		s.Owned("mf_b")
		s.Exec("GRANT mf_b TO mf_a WITH ADMIN TRUE, INHERIT FALSE, SET FALSE")
		s.Actor("mf_a")
		s.Exec("GRANT mf_b TO mf_a")
		s.Read(true)
		s.Actor("")
		s.Exec("GRANT mf_c TO mf_b WITH ADMIN TRUE")
		s.Actor("mf_a")
		s.ClearNotices()
		// REL_18_0 GrantRole/AddRoleMems make the first target's changed
		// INHERIT visible to inferred-grantor selection for the next target.
		// MEMBER authority survives, but there is then no INHERIT grantor.
		s.Fail("GRANT mf_b,mf_c TO mf_a WITH INHERIT FALSE", "XX000", "no possible grantors", "")
		if len(*s.Notices) != 0 {
			t.Fatal("candidate failure notices", *s.Notices)
		}
		// The statement rollback restores the original a-origin edge, not
		// merely the preceding target's object-privilege closure in memory.
		s.Read(true)
		s.Flags("mf_b", "mf_a", "mf_a", 1, false, true, true)
		s.Flags("mf_b", "mf_a", s.Bootstrap, 1, true, false, false)
		s.Flags("mf_c", "mf_a", "mf_b", 0, false, false, false)
		s.Exec("GRANT mf_c TO mf_a")
		s.Flags("mf_c", "mf_a", "mf_b", 1, false, true, true)
		s.ClearNotices()
		s.Exec("REVOKE mf_c FROM mf_a GRANTED BY mf_a")
		s.Notice("WARNING", "01000", `role "mf_a" has not been granted membership in role "mf_c" by role "mf_a"`)
		s.Exec("REVOKE mf_c FROM mf_a GRANTED BY mf_b")
		s.Flags("mf_c", "mf_a", "mf_b", 0, false, false, false)
		s.Actor("")
		s.Exec("REVOKE mf_b FROM mf_a GRANTED BY mf_a")
		s.Actor("mf_a")
		s.Read(false)
		s.Fail("GRANT mf_c TO mf_a", "XX000", "no possible grantors", "")
	})
}

func TestReferenceRoleMembershipBatchSanityBeforeAdmin(t *testing.T) {
	fixture.RoleMembershipCases(t, []string{"mg_r", "mg_a"}, func(s *fixture.RoleMembershipScenario) {
		t := s.T
		s.Owned("mg_r")
		s.Exec("GRANT mg_r TO mg_a WITH ADMIN TRUE")
		s.ClearNotices()
		// AddRoleMems checks EVERY recipient's role loop before considering
		// ADMIN support. Self membership wins over the proposed backgrant.
		s.Fail("GRANT mg_r TO mg_a,mg_r WITH ADMIN TRUE GRANTED BY mg_a", "0LP01", `role "mg_r" is a member of role "mg_r"`, "")
		if len(*s.Notices) != 0 {
			t.Fatal("sanity-before-admin notices", *s.Notices)
		}
		s.Flags("mg_r", "mg_a", s.Bootstrap, 1, true, true, true)
		s.Flags("mg_r", "mg_a", "mg_a", 0, false, false, false)
		s.Actor("mg_a")
		s.Read(true)
	})
}

func TestReferenceRoleMembershipBatchSanityBeforeNotice(t *testing.T) {
	fixture.RoleMembershipCases(t, []string{"mh_r", "mh_a"}, func(s *fixture.RoleMembershipScenario) {
		t := s.T
		s.Owned("mh_r")
		s.Exec("GRANT mh_r TO mh_a")
		s.ClearNotices()
		// The preliminary recipient scan must reject mh_r before emitting
		// an already-granted NOTICE for the preceding unchanged mh_a edge.
		s.Fail("GRANT mh_r TO mh_a,mh_r", "0LP01", `role "mh_r" is a member of role "mh_r"`, "")
		if len(*s.Notices) != 0 {
			t.Fatal("sanity-before-update notices", *s.Notices)
		}
		s.Flags("mh_r", "mh_a", s.Bootstrap, 1, false, true, true)
		s.Flags("mh_r", "mh_r", s.Bootstrap, 0, false, false, false)
		s.Exec("GRANT mh_r TO mh_a")
		s.Notice("NOTICE", "00000", fmt.Sprintf(`role "mh_a" has already been granted membership in role "mh_r" by role "%s"`, s.Bootstrap))
		s.Actor("mh_a")
		s.Read(true)
	})
}

func TestReferenceRoleMembershipBatchRevokeWarningOrder(t *testing.T) {
	for _, recipients := range []string{"mi_a,mi_c", "mi_c,mi_a"} {
		t.Run(recipients, func(t *testing.T) {
			fixture.RoleMembershipCases(t, []string{"mi_r", "mi_a", "mi_b", "mi_c"}, func(s *fixture.RoleMembershipScenario) {
				t := s.T
				s.Owned("mi_r")
				s.Exec("GRANT mi_r TO mi_a WITH ADMIN TRUE")
				s.Actor("mi_a")
				s.Exec("GRANT mi_r TO mi_b")
				s.Actor("")
				s.ClearNotices()
				// DelRoleMems plans recursive RESTRICT per recipient in order.
				// It never reaches c after a's dependency failure, but a warning
				// already emitted for c in the reverse order remains delivered.
				s.Restrict("REVOKE mi_r FROM " + recipients + " RESTRICT")
				if recipients == "mi_a,mi_c" {
					if len(*s.Notices) != 0 {
						t.Fatal("warning for unreached recipient", *s.Notices)
					}
				} else {
					s.Notice("WARNING", "01000", fmt.Sprintf(`role "mi_c" has not been granted membership in role "mi_r" by role "%s"`, s.Bootstrap))
				}
				s.Flags("mi_r", "mi_a", s.Bootstrap, 1, true, true, true)
				s.Flags("mi_r", "mi_b", "mi_a", 1, false, true, true)
				s.Flags("mi_r", "mi_c", s.Bootstrap, 0, false, false, false)
				s.Actor("mi_b")
				s.Read(true)
				s.Actor("mi_a")
				s.Exec("GRANT mi_r TO mi_c")
				s.Actor("mi_c")
				s.Read(true)
				s.Actor("")
				s.Exec("REVOKE mi_r FROM mi_a CASCADE")
				s.Flags("mi_r", "mi_a", s.Bootstrap, 0, false, false, false)
				s.Flags("mi_r", "mi_b", "mi_a", 0, false, false, false)
				s.Flags("mi_r", "mi_c", "mi_a", 0, false, false, false)
				s.Actor("mi_b")
				s.Read(false)
				s.Actor("mi_c")
				s.Read(false)
			})
		})
	}
}

func TestReferenceRoleMembershipBatchRevokeAuthority(t *testing.T) {
	fixture.RoleMembershipCases(t, []string{"mr_a", "mr_p", "mr_q", "mr_g"}, func(s *fixture.RoleMembershipScenario) {
		t := s.T
		s.Owned("mr_p")
		s.Exec("GRANT mr_p TO mr_g WITH ADMIN TRUE")
		s.Exec("GRANT mr_p TO mr_a WITH ADMIN TRUE GRANTED BY mr_g")
		s.Exec("GRANT mr_g TO mr_a")
		s.Exec("GRANT mr_q TO mr_p WITH ADMIN TRUE")
		// ADMIN FALSE is an update, not a dependent revoke. Removing the
		// now non-ADMIN bootstrap row leaves the independent g-origin grant.
		// This uses the existing AdminFalseGrantorCleanup release contract.
		s.Exec("GRANT mr_p TO mr_g WITH ADMIN FALSE")
		s.Exec("REVOKE mr_p FROM mr_g")
		s.Actor("mr_a")
		s.Read(true)
		s.Flags("mr_p", "mr_a", "mr_g", 1, true, true, true)
		s.Flags("mr_p", "mr_g", s.Bootstrap, 0, false, false, false)
		s.ClearNotices()
		message, detail := fixture.RoleMembershipDenied("mr_q", "revoke")
		// Unlike AddRoleMems, DelRoleMems has no per-target command-counter
		// increment: this REVOKE authorizes both targets using statement-entry
		// authority, even though its first target removes a's only p path.
		// Explicit g selects that real row; q's absent g-origin row warns.
		s.Exec("REVOKE mr_p,mr_q FROM mr_a GRANTED BY mr_g")
		s.Notice("WARNING", "01000", `role "mr_a" has not been granted membership in role "mr_q" by role "mr_g"`)
		s.Read(false)
		s.Flags("mr_p", "mr_a", "mr_g", 0, false, false, false)
		s.Flags("mr_q", "mr_p", s.Bootstrap, 1, true, true, true)
		s.Flags("mr_q", "mr_a", "mr_g", 0, false, false, false)
		// A separate statement refreshes authority: p is now unavailable,
		// so q's ADMIN holder is unreachable and this REVOKE is denied.
		s.Fail("REVOKE mr_q FROM mr_a GRANTED BY mr_g", "42501", message, detail)
		if len(*s.Notices) != 0 {
			t.Fatal("denied next statement notices", *s.Notices)
		}
	})
}

func TestReferenceRoleMembershipBatchRevokeDuplicateTargets(t *testing.T) {
	fixture.RoleMembershipCases(t, []string{"mt_role", "mt_member"}, func(s *fixture.RoleMembershipScenario) {
		t := s.T
		s.Owned("mt_role")
		s.Exec("GRANT mt_role TO mt_member")
		s.Actor("mt_member")
		s.Read(true)
		s.Actor("")
		s.ClearNotices()
		// DelRoleMems does not command-counter-increment between targets.
		// A second mutation of the same original tuple errors and rolls back.
		s.Fail("REVOKE mt_role,mt_role FROM mt_member", "XX000", "tuple already updated by self", "")
		if len(*s.Notices) != 0 {
			t.Fatal("repeated target original row warning", *s.Notices)
		}
		s.Flags("mt_role", "mt_member", s.Bootstrap, 1, false, true, true)
		s.Actor("mt_member")
		s.Read(true)
		s.Actor("")
		s.Exec("REVOKE mt_role FROM mt_member")
		s.Flags("mt_role", "mt_member", s.Bootstrap, 0, false, false, false)
		s.Actor("mt_member")
		s.Read(false)
	})
}
