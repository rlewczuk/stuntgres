package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceRoleMembershipBatchCandidateFreshness(t *testing.T) {
	membershipCases(t, []string{"mf_a", "mf_b", "mf_c"}, func(s *membershipScenario) {
		t := s.t
		s.owned("mf_b")
		s.exec("GRANT mf_b TO mf_a WITH ADMIN TRUE, INHERIT FALSE, SET FALSE")
		s.actor("mf_a")
		s.exec("GRANT mf_b TO mf_a")
		s.read(true)
		s.actor("")
		s.exec("GRANT mf_c TO mf_b WITH ADMIN TRUE")
		s.actor("mf_a")
		s.clearNotices()
		// REL_18_0 GrantRole/AddRoleMems make the first target's changed
		// INHERIT visible to inferred-grantor selection for the next target.
		// MEMBER authority survives, but there is then no INHERIT grantor.
		s.fail("GRANT mf_b,mf_c TO mf_a WITH INHERIT FALSE", "XX000", "no possible grantors", "")
		if len(*s.notices) != 0 {
			t.Fatal("candidate failure notices", *s.notices)
		}
		// The statement rollback restores the original a-origin edge, not
		// merely the preceding target's object-privilege closure in memory.
		s.read(true)
		s.flags("mf_b", "mf_a", "mf_a", 1, false, true, true)
		s.flags("mf_b", "mf_a", s.bootstrap, 1, true, false, false)
		s.flags("mf_c", "mf_a", "mf_b", 0, false, false, false)
		s.exec("GRANT mf_c TO mf_a")
		s.flags("mf_c", "mf_a", "mf_b", 1, false, true, true)
		s.clearNotices()
		s.exec("REVOKE mf_c FROM mf_a GRANTED BY mf_a")
		s.notice("WARNING", "01000", `role "mf_a" has not been granted membership in role "mf_c" by role "mf_a"`)
		s.exec("REVOKE mf_c FROM mf_a GRANTED BY mf_b")
		s.flags("mf_c", "mf_a", "mf_b", 0, false, false, false)
		s.actor("")
		s.exec("REVOKE mf_b FROM mf_a GRANTED BY mf_a")
		s.actor("mf_a")
		s.read(false)
		s.fail("GRANT mf_c TO mf_a", "XX000", "no possible grantors", "")
	})
}

func TestReferenceRoleMembershipBatchSanityBeforeAdmin(t *testing.T) {
	membershipCases(t, []string{"mg_r", "mg_a"}, func(s *membershipScenario) {
		t := s.t
		s.owned("mg_r")
		s.exec("GRANT mg_r TO mg_a WITH ADMIN TRUE")
		s.clearNotices()
		// AddRoleMems checks EVERY recipient's role loop before considering
		// ADMIN support. Self membership wins over the proposed backgrant.
		s.fail("GRANT mg_r TO mg_a,mg_r WITH ADMIN TRUE GRANTED BY mg_a", "0LP01", `role "mg_r" is a member of role "mg_r"`, "")
		if len(*s.notices) != 0 {
			t.Fatal("sanity-before-admin notices", *s.notices)
		}
		s.flags("mg_r", "mg_a", s.bootstrap, 1, true, true, true)
		s.flags("mg_r", "mg_a", "mg_a", 0, false, false, false)
		s.actor("mg_a")
		s.read(true)
	})
}

func TestReferenceRoleMembershipBatchSanityBeforeNotice(t *testing.T) {
	membershipCases(t, []string{"mh_r", "mh_a"}, func(s *membershipScenario) {
		t := s.t
		s.owned("mh_r")
		s.exec("GRANT mh_r TO mh_a")
		s.clearNotices()
		// The preliminary recipient scan must reject mh_r before emitting
		// an already-granted NOTICE for the preceding unchanged mh_a edge.
		s.fail("GRANT mh_r TO mh_a,mh_r", "0LP01", `role "mh_r" is a member of role "mh_r"`, "")
		if len(*s.notices) != 0 {
			t.Fatal("sanity-before-update notices", *s.notices)
		}
		s.flags("mh_r", "mh_a", s.bootstrap, 1, false, true, true)
		s.flags("mh_r", "mh_r", s.bootstrap, 0, false, false, false)
		s.exec("GRANT mh_r TO mh_a")
		s.notice("NOTICE", "00000", fmt.Sprintf(`role "mh_a" has already been granted membership in role "mh_r" by role "%s"`, s.bootstrap))
		s.actor("mh_a")
		s.read(true)
	})
}

func TestReferenceRoleMembershipBatchRevokeWarningOrder(t *testing.T) {
	for _, recipients := range []string{"mi_a,mi_c", "mi_c,mi_a"} {
		t.Run(recipients, func(t *testing.T) {
			membershipCases(t, []string{"mi_r", "mi_a", "mi_b", "mi_c"}, func(s *membershipScenario) {
				t := s.t
				s.owned("mi_r")
				s.exec("GRANT mi_r TO mi_a WITH ADMIN TRUE")
				s.actor("mi_a")
				s.exec("GRANT mi_r TO mi_b")
				s.actor("")
				s.clearNotices()
				// DelRoleMems plans recursive RESTRICT per recipient in order.
				// It never reaches c after a's dependency failure, but a warning
				// already emitted for c in the reverse order remains delivered.
				s.restrict("REVOKE mi_r FROM " + recipients + " RESTRICT")
				if recipients == "mi_a,mi_c" {
					if len(*s.notices) != 0 {
						t.Fatal("warning for unreached recipient", *s.notices)
					}
				} else {
					s.notice("WARNING", "01000", fmt.Sprintf(`role "mi_c" has not been granted membership in role "mi_r" by role "%s"`, s.bootstrap))
				}
				s.flags("mi_r", "mi_a", s.bootstrap, 1, true, true, true)
				s.flags("mi_r", "mi_b", "mi_a", 1, false, true, true)
				s.flags("mi_r", "mi_c", s.bootstrap, 0, false, false, false)
				s.actor("mi_b")
				s.read(true)
				s.actor("mi_a")
				s.exec("GRANT mi_r TO mi_c")
				s.actor("mi_c")
				s.read(true)
				s.actor("")
				s.exec("REVOKE mi_r FROM mi_a CASCADE")
				s.flags("mi_r", "mi_a", s.bootstrap, 0, false, false, false)
				s.flags("mi_r", "mi_b", "mi_a", 0, false, false, false)
				s.flags("mi_r", "mi_c", "mi_a", 0, false, false, false)
				s.actor("mi_b")
				s.read(false)
				s.actor("mi_c")
				s.read(false)
			})
		})
	}
}

func TestReferenceRoleMembershipBatchRevokeAuthority(t *testing.T) {
	membershipCases(t, []string{"mr_a", "mr_p", "mr_q", "mr_g"}, func(s *membershipScenario) {
		t := s.t
		s.owned("mr_p")
		s.exec("GRANT mr_p TO mr_g WITH ADMIN TRUE")
		s.exec("GRANT mr_p TO mr_a WITH ADMIN TRUE GRANTED BY mr_g")
		s.exec("GRANT mr_g TO mr_a")
		s.exec("GRANT mr_q TO mr_p WITH ADMIN TRUE")
		// ADMIN FALSE is an update, not a dependent revoke. Removing the
		// now non-ADMIN bootstrap row leaves the independent g-origin grant.
		// This uses the existing AdminFalseGrantorCleanup release contract.
		s.exec("GRANT mr_p TO mr_g WITH ADMIN FALSE")
		s.exec("REVOKE mr_p FROM mr_g")
		s.actor("mr_a")
		s.read(true)
		s.flags("mr_p", "mr_a", "mr_g", 1, true, true, true)
		s.flags("mr_p", "mr_g", s.bootstrap, 0, false, false, false)
		s.clearNotices()
		message, detail := membershipDenied("mr_q", "revoke")
		// Unlike AddRoleMems, DelRoleMems has no per-target command-counter
		// increment: this REVOKE authorizes both targets using statement-entry
		// authority, even though its first target removes a's only p path.
		// Explicit g selects that real row; q's absent g-origin row warns.
		s.exec("REVOKE mr_p,mr_q FROM mr_a GRANTED BY mr_g")
		s.notice("WARNING", "01000", `role "mr_a" has not been granted membership in role "mr_q" by role "mr_g"`)
		s.read(false)
		s.flags("mr_p", "mr_a", "mr_g", 0, false, false, false)
		s.flags("mr_q", "mr_p", s.bootstrap, 1, true, true, true)
		s.flags("mr_q", "mr_a", "mr_g", 0, false, false, false)
		// A separate statement refreshes authority: p is now unavailable,
		// so q's ADMIN holder is unreachable and this REVOKE is denied.
		s.fail("REVOKE mr_q FROM mr_a GRANTED BY mr_g", "42501", message, detail)
		if len(*s.notices) != 0 {
			t.Fatal("denied next statement notices", *s.notices)
		}
	})
}

func TestReferenceRoleMembershipBatchRevokeDuplicateTargets(t *testing.T) {
	membershipCases(t, []string{"mt_role", "mt_member"}, func(s *membershipScenario) {
		t := s.t
		s.owned("mt_role")
		s.exec("GRANT mt_role TO mt_member")
		s.actor("mt_member")
		s.read(true)
		s.actor("")
		s.clearNotices()
		// DelRoleMems does not command-counter-increment between targets.
		// A second mutation of the same original tuple errors and rolls back.
		s.fail("REVOKE mt_role,mt_role FROM mt_member", "XX000", "tuple already updated by self", "")
		if len(*s.notices) != 0 {
			t.Fatal("repeated target original row warning", *s.notices)
		}
		s.flags("mt_role", "mt_member", s.bootstrap, 1, false, true, true)
		s.actor("mt_member")
		s.read(true)
		s.actor("")
		s.exec("REVOKE mt_role FROM mt_member")
		s.flags("mt_role", "mt_member", s.bootstrap, 0, false, false, false)
		s.actor("mt_member")
		s.read(false)
	})
}
