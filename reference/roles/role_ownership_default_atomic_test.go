package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoleOwnershipDefaultRestrictAtomicity(t *testing.T) {
	for _, mode := range []string{"recipient_only", "creator_policy", "creator_stored_empty"} {
		t.Run(mode, func(t *testing.T) {
			ownershipCases(t, func(s *membershipScenario) {
				ownershipDefaultSchemas(s)
				ownershipDefaultTable(s, "own_default_root", "own_source")
				s.exec("GRANT SELECT ON own_default_root TO own_other,own_reader")
				s.actor("own_other")
				fixture.WithPublicSchemaOwner(s.t, s.p, s.backend, "own_other", func() { s.exec("CREATE VIEW own_default_external AS SELECT id FROM own_default_root") })
				s.actor("")
				s.actor("own_other")
				fixture.WithPublicSchemaOwner(s.t, s.p, s.backend, "own_other", func() { s.exec("CREATE TABLE own_default_sentinel(id INT)") })
				s.run(fixture.Exec("INSERT INTO own_default_sentinel VALUES(9)", 1))
				s.actor("")
				if mode == "creator_policy" {
					ownershipDefault(s, "own_source", "", "GRANT SELECT ON TABLES TO own_reader")
				}
				if mode == "creator_stored_empty" {
					ownershipDefault(s, "own_source", "", "REVOKE ALL ON FUNCTIONS FROM own_source,PUBLIC")
				}
				ownershipDefault(s, "own_alt", "own_schema", "GRANT SELECT ON TABLES TO own_source WITH GRANT OPTION")
				ownershipDefault(s, "own_alt", "", "GRANT INSERT ON TABLES TO own_reader")
				ownershipDefaultTable(s, "own_schema.own_received", "own_alt")
				s.actor("own_source")
				s.exec("GRANT SELECT ON own_schema.own_received TO own_downstream")
				s.actor("")
				ownershipDefault(s, "own_other", "", "GRANT USAGE ON SEQUENCES TO own_source")
				s.actor("own_other")
				fixture.WithPublicSchemaOwner(s.t, s.p, s.backend, "own_other", func() { s.exec("CREATE SEQUENCE own_default_clock START 7") })
				s.actor("own_source")
				ownershipSelect(s, "SELECT nextval('own_default_clock') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(7)}})
				s.actor("")
				var rootID, viewID, clockID int64
				if err := s.c.QueryRowContext(s.p.Ctx, "SELECT 'own_default_root'::regclass::oid::BIGINT,'own_default_external'::regclass::oid::BIGINT,'own_default_clock'::regclass::oid::BIGINT").Scan(&rootID, &viewID, &clockID); err != nil || rootID <= 0 || viewID <= 0 || clockID <= 0 {
					s.t.Fatal("atomic identities", rootID, viewID, clockID, err)
				}
				var sentinelID int64
				if err := s.c.QueryRowContext(s.p.Ctx, "SELECT 'own_default_sentinel'::regclass::oid::BIGINT").Scan(&sentinelID); err != nil || sentinelID <= 0 {
					s.t.Fatal("sentinel identity", sentinelID, err)
				}
				sourceRecords := 0
				if mode != "recipient_only" {
					sourceRecords = 1
				}
				defaultIDs := map[string][]int64{"own_source": ownershipDefaultIDs(s, "own_source", sourceRecords), "own_alt": ownershipDefaultIDs(s, "own_alt", 2), "own_other": ownershipDefaultIDs(s, "own_other", 1)}
				message := "cannot drop table own_default_root because other objects depend on it"
				if mode != "recipient_only" {
					message = "cannot drop desired object(s) because other objects depend on them"
				}
				s.clearNotices()
				ownershipFailure(s, "DROP OWNED BY own_source RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: message, Detail: "view own_default_external depends on table own_default_root", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
				ownershipNotices(s)
				ownershipOwner(s, "own_default_root", "own_source")
				ownershipRows(s, "own_default_root")
				ownershipRows(s, "own_default_external")
				ownershipSelect(s, "SELECT 'own_default_root'::regclass::oid::BIGINT AS root,'own_default_external'::regclass::oid::BIGINT AS view,'own_default_clock'::regclass::oid::BIGINT AS clock", []string{"root", "view", "clock"}, []string{"INT8", "INT8", "INT8"}, [][]any{{rootID, viewID, clockID}})
				ownershipSelect(s, "SELECT 'own_default_sentinel'::regclass::oid::BIGINT AS oid", []string{"oid"}, []string{"INT8"}, [][]any{{sentinelID}})
				ownershipSelect(s, "SELECT id FROM own_default_sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}})
				for creator, ids := range defaultIDs {
					ownershipDefaultStable(s, creator, ids)
				}
				ownershipDefaultKeyCount(s, "own_alt", "own_schema", "r", 1)
				ownershipDefaultKeyCount(s, "own_other", "", "S", 1)
				if mode == "creator_policy" {
					ownershipDefaultKeyCount(s, "own_source", "", "r", 1)
				}
				if mode == "creator_stored_empty" {
					ownershipDefaultKeyCount(s, "own_source", "", "f", 1)
				}
				ownershipDefaultProjection(s, "table_name='own_received' AND grantee='own_source'", [][]any{{"own_alt", "own_source", "own_schema", "own_received", "SELECT", "YES", "YES"}})
				s.actor("own_reader")
				ownershipRows(s, "own_default_root")
				s.run(fixture.Exec("INSERT INTO own_schema.own_received VALUES(9)", 1))
				s.actor("own_downstream")
				ownershipSelect(s, "SELECT id FROM own_schema.own_received ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(9)}})
				s.actor("own_source")
				ownershipSelect(s, "SELECT currval('own_default_clock') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(7)}})
				ownershipSelect(s, "SELECT nextval('own_default_clock') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(8)}})
				s.actor("")
				// Actual future creation proves the veto retained the original recipient key.
				ownershipDefaultTable(s, "own_schema.own_after_veto", "own_alt")
				s.actor("own_source")
				ownershipRows(s, "own_schema.own_after_veto")
				s.actor("")
				if mode == "creator_policy" {
					ownershipDefaultTable(s, "own_default_policy_after_veto", "own_source")
					s.actor("own_reader")
					ownershipRows(s, "own_default_policy_after_veto")
					s.actor("")
					s.exec("ALTER TABLE own_default_policy_after_veto OWNER TO own_target")
				}
				if mode == "creator_stored_empty" {
					ownershipRoleDependency(s, "own_source", "owner of table own_default_root", ownershipDefaultLine("own_source", "", "functions", true), ownershipDefaultLine("own_alt", "own_schema", "relations", false), ownershipDefaultLine("own_other", "", "sequences", false), "privileges for table own_schema.own_received", "privileges for table own_schema.own_after_veto", "privileges for sequence own_default_clock")
				}
				s.clearNotices()
				s.exec("DROP OWNED BY own_source CASCADE")
				ownershipNotices(s, ownershipCascadeNotice("drop cascades to view own_default_external", ""))
				ownershipCount(s, "SELECT count(*) FROM pg_class WHERE relname='own_default_root' OR relname='own_default_external'", 0)
				ownershipDefaultKeyCount(s, "own_alt", "own_schema", "r", 0)
				ownershipDefaultKeyCount(s, "own_other", "", "S", 0)
				ownershipDefaultProjection(s, "table_name='own_received' AND (grantee='own_source' OR grantee='own_downstream')", nil)
				for _, role := range []string{"own_source", "own_downstream"} {
					s.actor(role)
					ownershipDefaultDenied(s, "own_schema.own_received")
				}
				s.actor("own_source")
				ownershipFailure(s, "SELECT nextval('own_default_clock') AS n", fixture.Diagnostic{Code: "42501", Message: "permission denied for sequence own_default_clock"})
				s.actor("")
				ownershipOwner(s, "own_default_clock", "own_other")
				ownershipOwner(s, "own_default_sentinel", "own_other")
				ownershipSelect(s, "SELECT 'own_default_sentinel'::regclass::oid::BIGINT AS oid", []string{"oid"}, []string{"INT8"}, [][]any{{sentinelID}})
				ownershipSelect(s, "SELECT id FROM own_default_sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}})
				s.actor("own_reader")
				s.run(fixture.Exec("INSERT INTO own_schema.own_received VALUES(11)", 1))
				s.actor("")
				ownershipSelect(s, "SELECT id FROM own_schema.own_received ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(9)}, {int64(11)}})
				ownershipDefaultTable(s, "own_schema.own_after_cleanup", "own_alt")
				s.actor("own_source")
				ownershipDefaultDenied(s, "own_schema.own_after_cleanup")
				s.actor("")
				s.exec("DROP ROLE own_source")
			})
		})
	}
}
