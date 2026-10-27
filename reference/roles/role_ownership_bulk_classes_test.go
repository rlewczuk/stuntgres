package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleOwnershipReassignClassEligibility(t *testing.T) {
	ownershipCases(t, func(s *fixture.RoleMembershipScenario) {
		// Schema belongs to an independent owner, never a source bulk root.
		// INHERIT supplies genuine schema lookup/CREATE without schema-GRANT SQL.
		s.Exec("CREATE SCHEMA own_class AUTHORIZATION own_middle")
		s.Exec("GRANT own_middle TO own_source WITH INHERIT TRUE, SET FALSE")
		s.Exec("GRANT own_source TO own_actor WITH INHERIT TRUE, SET FALSE")
		s.Exec("GRANT own_target TO own_actor WITH INHERIT TRUE, SET TRUE")
		s.Actor("own_source")
		s.Exec("CREATE COLLATION own_class.coll FROM \"C\"")
		s.Actor("own_actor")
		ownershipFailure(s, "REASSIGN OWNED BY own_source TO own_target", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema own_class"})
		s.Actor("")
		s.Exec("GRANT own_middle TO own_target WITH INHERIT TRUE, SET FALSE")
		s.Actor("own_actor")
		s.Exec("REASSIGN OWNED BY own_source TO own_target")
		s.Actor("own_target")
		s.Exec("ALTER COLLATION own_class.coll RENAME TO coll_moved")
		s.Actor("")
		s.Exec("DROP COLLATION own_class.coll_moved")
		s.Exec("REVOKE own_middle FROM own_target")
		s.Actor("own_source")
		s.Exec("CREATE TABLE own_class.t(id INT)")
		s.Run(fixture.Exec("INSERT INTO own_class.t VALUES(7)", 1))
		s.Exec("CREATE TYPE own_class.e AS ENUM('a')")
		s.Actor("own_actor")
		s.Exec("REASSIGN OWNED BY own_source TO own_target")
		s.Actor("")
		ownershipOwner(s, "own_class.t", "own_target")
		ownershipRows(s, "own_class.t")
		// Bulk succeeded while successor lacked schema CREATE. Restore lookup
		// authority only now to exercise successor's independent type ownership.
		s.Exec("GRANT own_middle TO own_target WITH INHERIT TRUE, SET FALSE")
		s.Exec("GRANT " + s.Bootstrap + " TO own_target WITH INHERIT FALSE, SET TRUE")
		ownershipInherited(s, "own_target", s.Bootstrap, "USAGE", false)
		s.Actor("own_target")
		// A changed-owner utility proves genuine successor authority.
		s.Exec("ALTER TYPE own_class.e OWNER TO " + s.Bootstrap)
		s.Actor("")
		s.Exec("ALTER TYPE own_class.e OWNER TO own_target")
		s.Exec("DROP OWNED BY own_source CASCADE")
		s.Exec("DROP OWNED BY own_target CASCADE")
		s.Exec("DROP SCHEMA own_class")
	})
}
