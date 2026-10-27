package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoleOwnershipDefaultCleanup(t *testing.T) {
	t.Run("creator_only_all_keys", func(t *testing.T) {
		ownershipCases(t, func(s *fixture.RoleMembershipScenario) {
			ownershipDefaultSchemas(s)
			for _, creator := range []string{"own_source", "own_target"} {
				lines := ownershipDefaultTen(s, creator, "own_schema", "own_reader")
				ownershipRoleDependency(s, creator, lines...)
			}
			ownershipDefault(s, "own_alt", "", "REVOKE ALL ON FUNCTIONS FROM own_alt,PUBLIC")
			s.ClearNotices()
			s.Exec("DROP OWNED BY own_source,own_target,own_source RESTRICT")
			ownershipNotices(s)
			s.Exec("DROP OWNED BY own_source,own_target RESTRICT")
			s.Exec("DROP ROLE own_source,own_target")
			ownershipRoleDependency(s, "own_alt", ownershipDefaultLine("own_alt", "", "functions", true))
			s.Exec("DROP OWNED BY own_alt RESTRICT")
			s.Exec("DROP ROLE own_alt")
		})
	})
	t.Run("reassigned_objects_keep_materialized_acl", func(t *testing.T) {
		ownershipCases(t, func(s *fixture.RoleMembershipScenario) {
			ownershipDefault(s, "own_source", "", "GRANT SELECT ON TABLES TO own_reader")
			ownershipDefaultTable(s, "own_default_old", "own_source")
			s.Exec("REASSIGN OWNED BY own_source TO own_target")
			ownershipRoleDependency(s, "own_source", ownershipDefaultLine("own_source", "", "relations", true))
			s.Exec("DROP OWNED BY own_source RESTRICT")
			s.Exec("DROP ROLE own_source")
			ownershipOwner(s, "own_default_old", "own_target")
			s.Actor("own_reader")
			ownershipRows(s, "own_default_old")
			s.Actor("")
			s.Exec("CREATE ROLE own_source LOGIN")
			ownershipDefaultTable(s, "own_default_recreated_creator", "own_source")
			s.Actor("own_reader")
			ownershipDefaultDenied(s, "own_default_recreated_creator")
			ownershipRows(s, "own_default_old")
			s.Actor("")
		})
	})
	t.Run("recipients_all_classes_and_key_baselines", func(t *testing.T) {
		ownershipCases(t, func(s *fixture.RoleMembershipScenario) {
			ownershipDefaultSchemas(s)
			for _, c := range ownershipDefaultClasses {
				ownershipDefault(s, "own_alt", "", "GRANT ALL ON "+c.sql+" TO own_source,own_target WITH GRANT OPTION")
				ownershipDefault(s, "own_alt", "", "GRANT ALL ON "+c.sql+" TO own_reader")
			}
			ownershipDefault(s, "own_alt", "", "GRANT SELECT ON TABLES TO PUBLIC")
			for _, c := range ownershipDefaultClasses[:4] {
				ownershipDefault(s, "own_alt", "own_schema", "GRANT ALL ON "+c.sql+" TO own_source,own_target WITH GRANT OPTION")
				ownershipDefault(s, "own_alt", "own_schema", "GRANT ALL ON "+c.sql+" TO own_reader")
			}
			// A selected-only scoped key normalizes to absence; a global stored empty
			// FUNCTIONS ACL is nonbaseline and must remain creator-owned.
			ownershipDefault(s, "own_other", "own_bulk", "GRANT SELECT ON TABLES TO own_source")
			ownershipDefault(s, "own_other", "", "REVOKE ALL ON FUNCTIONS FROM own_other,PUBLIC")
			ownershipDefault(s, "own_other", "", "GRANT EXECUTE ON FUNCTIONS TO own_source")
			var sourceLines, targetLines []string
			for _, c := range ownershipDefaultClasses {
				sourceLines = append(sourceLines, ownershipDefaultLine("own_alt", "", c.noun, false))
				targetLines = append(targetLines, ownershipDefaultLine("own_alt", "", c.noun, false))
			}
			for _, c := range ownershipDefaultClasses[:4] {
				sourceLines = append(sourceLines, ownershipDefaultLine("own_alt", "own_schema", c.noun, false))
				targetLines = append(targetLines, ownershipDefaultLine("own_alt", "own_schema", c.noun, false))
			}
			sourceLines = append(sourceLines, ownershipDefaultLine("own_other", "own_bulk", "relations", false), ownershipDefaultLine("own_other", "", "functions", false))
			ownershipRoleDependency(s, "own_source", sourceLines...)
			ownershipRoleDependency(s, "own_target", targetLines...)
			altIDs := ownershipDefaultIDs(s, "own_alt", 10)
			s.ClearNotices()
			s.Exec("DROP OWNED BY own_source,own_target RESTRICT")
			ownershipNotices(s)
			s.Exec("DROP OWNED BY own_source RESTRICT")
			ownershipDefaultKeyCount(s, "own_other", "own_bulk", "r", 0)
			ownershipDefaultKeyCount(s, "own_other", "", "f", 1)
			ownershipRoleDependency(s, "own_other", ownershipDefaultLine("own_other", "", "functions", true))
			ownershipDefaultStable(s, "own_alt", altIDs)
			var altLines []string
			for _, c := range ownershipDefaultClasses {
				altLines = append(altLines, ownershipDefaultLine("own_alt", "", c.noun, true))
			}
			for _, c := range ownershipDefaultClasses[:4] {
				altLines = append(altLines, ownershipDefaultLine("own_alt", "own_schema", c.noun, true))
			}
			ownershipRoleDependency(s, "own_alt", altLines...)
			ownershipDefaultTable(s, "own_schema.own_after_recipient", "own_alt")
			// PUBLIC SELECT remains, but selected ALL/options cannot confer UPDATE.
			for _, role := range []string{"own_source", "own_target"} {
				s.Actor(role)
				ownershipRows(s, "own_schema.own_after_recipient")
				ownershipFailure(s, "UPDATE own_schema.own_after_recipient SET id=7", fixture.Diagnostic{Code: "42501", Message: "permission denied for table own_after_recipient"})
			}
			s.Actor("own_reader")
			ownershipRows(s, "own_schema.own_after_recipient")
			s.Run(fixture.Exec("INSERT INTO own_schema.own_after_recipient VALUES(9)", 1))
			s.Actor("")
			ownershipDefaultProjection(s, "table_schema='own_schema' AND table_name='own_after_recipient' AND (grantee='own_source' OR grantee='own_target')", nil)
			ownershipDefaultTable(s, "own_bulk.own_scoped_absent", "own_other")
			s.Actor("own_source")
			ownershipDefaultDenied(s, "own_bulk.own_scoped_absent")
			s.Actor("")
			// Remove no policies by manual reversal: real selected-recipient cleanup is
			// the only operation making these otherwise root-free roles droppable.
			s.Exec("DROP ROLE own_source,own_target")
		})
	})
}
