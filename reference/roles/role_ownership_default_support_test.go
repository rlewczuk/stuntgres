package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoleOwnershipDefaultGrantSupport(t *testing.T) {
	for _, inherited := range []bool{false, true} {
		t.Run(fmt.Sprintf("inherited_owner=%t", inherited), func(t *testing.T) {
			ownershipCases(t, func(s *membershipScenario) {
				ownershipDefault(s, "own_other", "", "GRANT SELECT,UPDATE ON TABLES TO own_source WITH GRANT OPTION")
				ownershipDefault(s, "own_other", "", "GRANT SELECT ON TABLES TO own_alt WITH GRANT OPTION")
				ownershipDefault(s, "own_other", "", "GRANT SELECT,INSERT ON TABLES TO own_reader")
				ownershipDefault(s, "own_other", "", "GRANT SELECT ON TABLES TO PUBLIC")
				ownershipDefaultTable(s, "own_default_delegated", "own_other")
				if inherited {
					s.exec("GRANT own_other TO own_source WITH INHERIT TRUE, SET FALSE")
				}
				s.actor("own_source")
				s.exec("GRANT SELECT,UPDATE ON own_default_delegated TO own_downstream")
				s.actor("own_alt")
				s.exec("GRANT SELECT ON own_default_delegated TO own_downstream")
				s.actor("")
				ownershipDefaultProjection(s, "table_name='own_default_delegated' AND grantee='own_downstream'", [][]any{{"own_alt", "own_downstream", "public", "own_default_delegated", "SELECT", "NO", "YES"}, {"own_source", "own_downstream", "public", "own_default_delegated", "SELECT", "NO", "YES"}, {"own_source", "own_downstream", "public", "own_default_delegated", "UPDATE", "NO", "NO"}})
				s.clearNotices()
				s.exec("DROP OWNED BY own_source RESTRICT")
				ownershipNotices(s)
				ownershipDefaultKeyCount(s, "own_other", "", "r", 1)
				ownershipDefaultTable(s, "own_default_future", "own_other")
				s.actor("own_source")
				ownershipRows(s, "own_default_future") // PUBLIC remains.
				if !inherited {
					ownershipFailure(s, "UPDATE own_default_future SET id=7", fixture.Diagnostic{Code: "42501", Message: "permission denied for table own_default_future"})
				}
				s.actor("own_downstream")
				ownershipRows(s, "own_default_delegated")
				if inherited {
					s.run(fixture.Exec("UPDATE own_default_delegated SET id=7", 1))
					s.actor("")
					ownershipRoleDependency(s, "own_source", "privileges for table own_default_delegated")
					ownershipDefaultProjection(s, "table_name='own_default_delegated' AND grantor='own_source'", [][]any{{"own_source", "own_downstream", "public", "own_default_delegated", "SELECT", "NO", "YES"}, {"own_source", "own_downstream", "public", "own_default_delegated", "UPDATE", "NO", "NO"}})
					s.exec("REVOKE own_other FROM own_source")
					s.actor("own_other")
					s.exec("GRANT SELECT,UPDATE ON own_default_delegated TO own_source WITH GRANT OPTION")
					s.actor("")
					s.exec("DROP OWNED BY own_source RESTRICT")
				} else {
					ownershipFailure(s, "UPDATE own_default_delegated SET id=7", fixture.Diagnostic{Code: "42501", Message: "permission denied for table own_default_delegated"})
					s.actor("")
				}
				ownershipDefaultProjection(s, "table_name='own_default_delegated' AND grantee='own_downstream'", [][]any{{"own_alt", "own_downstream", "public", "own_default_delegated", "SELECT", "NO", "YES"}})
				ownershipDefaultProjection(s, "table_name='own_default_delegated' AND (grantor='own_source' OR grantee='own_source')", nil)
				s.actor("own_downstream")
				ownershipRows(s, "own_default_delegated")
				ownershipFailure(s, "UPDATE own_default_delegated SET id=7", fixture.Diagnostic{Code: "42501", Message: "permission denied for table own_default_delegated"})
				s.actor("own_reader")
				s.run(fixture.Exec("INSERT INTO own_default_delegated VALUES(9)", 1))
				s.run(fixture.Exec("INSERT INTO own_default_future VALUES(9)", 1))
				s.actor("")
				ownershipOwner(s, "own_default_delegated", "own_other")
				ownershipSelect(s, "SELECT id FROM own_default_delegated ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(9)}})
				s.exec("DROP ROLE own_source")
			})
		})
	}
}
