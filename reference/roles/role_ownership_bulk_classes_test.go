package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleOwnershipReassignClassEligibility(t *testing.T) {
	ownershipCases(t, func(s *membershipScenario) {
		// Schema belongs to an independent owner, never a source bulk root.
		// INHERIT supplies genuine schema lookup/CREATE without schema-GRANT SQL.
		s.exec("CREATE SCHEMA own_class AUTHORIZATION own_middle")
		s.exec("GRANT own_middle TO own_source WITH INHERIT TRUE, SET FALSE")
		s.exec("GRANT own_source TO own_actor WITH INHERIT TRUE, SET FALSE")
		s.exec("GRANT own_target TO own_actor WITH INHERIT TRUE, SET TRUE")
		s.actor("own_source")
		s.exec("CREATE COLLATION own_class.coll FROM \"C\"")
		s.actor("own_actor")
		ownershipFailure(s, "REASSIGN OWNED BY own_source TO own_target", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema own_class"})
		s.actor("")
		s.exec("GRANT own_middle TO own_target WITH INHERIT TRUE, SET FALSE")
		s.actor("own_actor")
		s.exec("REASSIGN OWNED BY own_source TO own_target")
		s.actor("own_target")
		s.exec("ALTER COLLATION own_class.coll RENAME TO coll_moved")
		s.actor("")
		s.exec("DROP COLLATION own_class.coll_moved")
		s.exec("REVOKE own_middle FROM own_target")
		s.actor("own_source")
		s.exec("CREATE TABLE own_class.t(id INT)")
		s.run(fixture.Exec("INSERT INTO own_class.t VALUES(7)", 1))
		s.exec("CREATE TYPE own_class.e AS ENUM('a')")
		s.actor("own_actor")
		s.exec("REASSIGN OWNED BY own_source TO own_target")
		s.actor("")
		ownershipOwner(s, "own_class.t", "own_target")
		ownershipRows(s, "own_class.t")
		// Bulk succeeded while successor lacked schema CREATE. Restore lookup
		// authority only now to exercise successor's independent type ownership.
		s.exec("GRANT own_middle TO own_target WITH INHERIT TRUE, SET FALSE")
		s.exec("GRANT " + s.bootstrap + " TO own_target WITH INHERIT FALSE, SET TRUE")
		ownershipInherited(s, "own_target", s.bootstrap, "USAGE", false)
		s.actor("own_target")
		// A changed-owner utility proves genuine successor authority without
		// adding the absent DROP TYPE or type-rename grammar providers.
		s.exec("ALTER TYPE own_class.e OWNER TO " + s.bootstrap)
		s.actor("")
		s.exec("ALTER TYPE own_class.e OWNER TO own_target")
		s.exec("DROP OWNED BY own_source CASCADE")
		s.exec("DROP OWNED BY own_target CASCADE")
		s.exec("DROP SCHEMA own_class")
	})
}
