package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// PostgreSQL18 acl.c recursive_revoke consults aclmask for surviving grant
// options; inherited ownership supplies those options even after the explicit
// incoming ACL row is removed. Cleanup must not erase that supported grantor.
func TestReferenceRoleOwnershipInheritedGrantSupport(t *testing.T) {
	ownershipCases(t, func(s *membershipScenario) {
		ownershipSeed(s, "own_support", "own_other")
		s.exec("GRANT own_other TO own_source WITH INHERIT TRUE, SET FALSE")
		s.actor("own_other")
		s.exec("GRANT SELECT ON own_support TO own_source WITH GRANT OPTION")
		s.exec("GRANT SELECT ON own_support TO own_reader")
		s.actor("own_source")
		s.exec("GRANT SELECT ON own_support TO own_downstream")
		ownershipRows(s, "own_support")
		s.actor("own_downstream")
		ownershipRows(s, "own_support")
		s.actor("")
		if s.backend.PG {
			// The explicit direct source option wins select_best_grantor's first
			// full-match search; inherited ownership is later cascade support.
			ownershipSelect(s, "SELECT g.rolname FROM pg_class c CROSS JOIN LATERAL aclexplode(c.relacl) a JOIN pg_roles g ON g.oid=a.grantor JOIN pg_roles r ON r.oid=a.grantee WHERE c.oid='own_support'::regclass::oid AND r.rolname='own_downstream' AND a.privilege_type='SELECT'", []string{"rolname"}, []string{"NAME"}, [][]any{{"own_source"}})
		}
		s.clearNotices()
		s.exec("DROP OWNED BY own_source RESTRICT")
		ownershipNotices(s)
		ownershipOwner(s, "own_support", "own_other")
		ownershipRows(s, "own_support")
		if s.backend.PG {
			ownershipCount(s, "SELECT count(*) FROM pg_class c CROSS JOIN LATERAL aclexplode(c.relacl) a JOIN pg_roles r ON r.oid=a.grantee WHERE c.oid='own_support'::regclass::oid AND r.rolname='own_source'", 0)
			ownershipSelect(s, "SELECT g.rolname FROM pg_class c CROSS JOIN LATERAL aclexplode(c.relacl) a JOIN pg_roles g ON g.oid=a.grantor JOIN pg_roles r ON r.oid=a.grantee WHERE c.oid='own_support'::regclass::oid AND r.rolname='own_downstream' AND a.privilege_type='SELECT'", []string{"rolname"}, []string{"NAME"}, [][]any{{"own_source"}})
		}
		s.actor("own_source")
		ownershipRows(s, "own_support") // Actual inherited owner authority survives.
		s.actor("own_downstream")
		ownershipRows(s, "own_support") // Source-origin delegation is still supported.
		s.actor("own_reader")
		ownershipRows(s, "own_support") // Independent grant remains unrelated.
		s.actor("")
		ownershipRoleDependency(s, "own_source", "privileges for table own_support")
		s.exec("REVOKE own_other FROM own_source")
		s.actor("own_source")
		ownershipFailure(s, "SELECT id FROM own_support", fixture.Diagnostic{Code: "42501", Message: "permission denied for table own_support"})
		s.actor("own_downstream")
		ownershipRows(s, "own_support") // Revoking membership is not an ACL rewrite.
		// Re-establish a direct option, then remove it via admitted DROP OWNED.
		// Without inherited owner options, its actual cascade now retires the
		// source's unsupported downstream provenance as well.
		s.actor("own_other")
		s.exec("GRANT SELECT ON own_support TO own_source WITH GRANT OPTION")
		s.actor("")
		s.exec("DROP OWNED BY own_source RESTRICT")
		for _, role := range []string{"own_source", "own_downstream"} {
			s.actor(role)
			ownershipFailure(s, "SELECT id FROM own_support", fixture.Diagnostic{Code: "42501", Message: "permission denied for table own_support"})
		}
		s.actor("own_reader")
		ownershipRows(s, "own_support")
		s.actor("")
		ownershipOwner(s, "own_support", "own_other")
		ownershipRows(s, "own_support")
		s.exec("DROP ROLE own_source")
		ownershipBool(s, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='own_source')", false)
	})
}
