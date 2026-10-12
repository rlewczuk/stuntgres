package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoleOwnershipDefaultSchemaCleanup(t *testing.T) {
	t.Run("rename_auto_all_creators_recreate", func(t *testing.T) {
		ownershipCases(t, func(s *membershipScenario) {
			s.exec("CREATE SCHEMA own_schema AUTHORIZATION own_source")
			s.exec("CREATE SCHEMA own_bulk AUTHORIZATION own_other")
			for _, creator := range []string{"own_source", "own_target", "own_alt"} {
				for _, c := range ownershipDefaultClasses[:4] {
					ownershipDefault(s, creator, "own_schema", "GRANT ALL ON "+c.sql+" TO own_reader")
				}
			}
			ownershipDefault(s, "own_source", "", "GRANT SELECT ON TABLES TO own_reader")
			ownershipDefault(s, "own_target", "", "GRANT SELECT ON TABLES TO own_actor")
			ownershipDefault(s, "own_alt", "own_bulk", "GRANT INSERT ON TABLES TO own_reader")
			s.exec("ALTER SCHEMA own_schema RENAME TO own_schema_renamed")
			lines := []string{"owner of schema own_schema_renamed", ownershipDefaultLine("own_source", "", "relations", true)}
			for _, c := range ownershipDefaultClasses[:4] {
				lines = append(lines, ownershipDefaultLine("own_source", "own_schema_renamed", c.noun, true))
			}
			ownershipRoleDependency(s, "own_source", lines...)
			s.exec("ALTER SCHEMA own_schema_renamed OWNER TO own_middle")
			s.clearNotices()
			s.exec("DROP OWNED BY own_middle RESTRICT")
			ownershipNotices(s)
			ownershipRoleDependency(s, "own_source", ownershipDefaultLine("own_source", "", "relations", true))
			ownershipRoleDependency(s, "own_target", ownershipDefaultLine("own_target", "", "relations", true))
			ownershipRoleDependency(s, "own_alt", ownershipDefaultLine("own_alt", "own_bulk", "relations", true))
			for _, creator := range []string{"own_source", "own_target", "own_alt"} {
				for _, c := range ownershipDefaultClasses[:4] {
					ownershipDefaultKeyCount(s, creator, "own_schema_renamed", c.code, 0)
				}
			}
			s.exec("CREATE SCHEMA own_schema_renamed AUTHORIZATION own_other")
			for _, role := range []string{"own_source", "own_target", "own_alt", "own_reader", "own_actor"} {
				s.exec("GRANT own_other TO " + role + " WITH INHERIT TRUE, SET FALSE")
			}
			ownershipDefaultTable(s, "own_schema_renamed.own_recreated", "own_source")
			s.actor("own_reader")
			ownershipRows(s, "own_schema_renamed.own_recreated")
			ownershipFailure(s, "INSERT INTO own_schema_renamed.own_recreated VALUES(9)", fixture.Diagnostic{Code: "42501", Message: "permission denied for table own_recreated"})
			s.actor("")
			ownershipDefaultTable(s, "own_schema_renamed.own_successor_policy", "own_target")
			s.actor("own_actor")
			ownershipRows(s, "own_schema_renamed.own_successor_policy")
			s.actor("own_reader")
			ownershipDefaultDenied(s, "own_schema_renamed.own_successor_policy")
			s.actor("")
			ownershipDefaultTable(s, "own_bulk.own_other_namespace", "own_alt")
			s.actor("own_reader")
			s.run(fixture.Exec("INSERT INTO own_bulk.own_other_namespace VALUES(9)", 1))
			s.actor("")
		})
	})
	for _, selectedCreator := range []bool{false, true} {
		t.Run(fmt.Sprintf("schema_owner_default=%t", selectedCreator), func(t *testing.T) {
			ownershipCases(t, func(s *membershipScenario) {
				s.exec("CREATE SCHEMA own_schema AUTHORIZATION own_source")
				s.exec("GRANT own_source TO own_target WITH INHERIT TRUE, SET FALSE")
				ownershipDefaultTable(s, "own_schema.own_schema_root", "own_target")
				s.exec("CREATE VIEW own_schema_external AS SELECT id FROM own_schema.own_schema_root")
				creator := "own_alt"
				if selectedCreator {
					creator = "own_source"
				}
				ownershipDefault(s, creator, "own_schema", "GRANT EXECUTE ON FUNCTIONS TO own_reader")
				message := "cannot drop schema own_schema because other objects depend on it"
				if selectedCreator {
					message = "cannot drop desired object(s) because other objects depend on them"
				}
				s.clearNotices()
				ownershipFailureDetailBag(s, "DROP OWNED BY own_source RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: message, Detail: "table own_schema.own_schema_root depends on schema own_schema\nview own_schema_external depends on table own_schema.own_schema_root", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
				ownershipNotices(s)
				ownershipRows(s, "own_schema.own_schema_root")
				ownershipRows(s, "own_schema_external")
				ownershipOwner(s, "own_schema.own_schema_root", "own_target")
				ownershipDefaultKeyCount(s, creator, "own_schema", "f", 1)
				s.clearNotices()
				s.exec("DROP OWNED BY own_source CASCADE")
				ownershipNoticesDetailBag(s, ownershipCascadeNotice("drop cascades to 2 other objects", "drop cascades to table own_schema.own_schema_root\ndrop cascades to view own_schema_external"))
				ownershipCount(s, "SELECT count(*) FROM pg_class WHERE relname='own_schema_root' OR relname='own_schema_external'", 0)
				s.exec("DROP ROLE own_source")
				s.exec("CREATE SCHEMA own_schema AUTHORIZATION own_other")
				ownershipDefaultKeyCount(s, creator, "own_schema", "f", 0)
			})
		})
	}
}
