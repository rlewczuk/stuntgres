package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleOwnershipDeleteGroupCounts(t *testing.T) {
	for _, serial := range []bool{false, true} {
		t.Run(fmt.Sprintf("serial=%t", serial), func(t *testing.T) {
			ownershipCases(t, func(s *membershipScenario) {
				s.actor("own_source")
				idType := "INT"
				if serial {
					idType = "SERIAL"
				}
				fixture.WithPublicSchemaOwner(s.t, s.p, s.backend, "own_source", func() { s.exec("CREATE TABLE own_counted(id " + idType + ",payload INT DEFAULT 9)") })
				fixture.WithPublicSchemaOwner(s.t, s.p, s.backend, "own_source", func() { s.exec("CREATE INDEX own_counted_index ON own_counted(payload)") })
				s.run(fixture.Exec("INSERT INTO own_counted(id) VALUES(7)", 1))
				s.exec("GRANT SELECT ON own_counted TO own_other,own_reader")
				if serial {
					ownershipSelect(s, "SELECT nextval('own_counted_id_seq') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}})
				}
				s.actor("own_other")
				fixture.WithPublicSchemaOwner(s.t, s.p, s.backend, "own_other", func() {
					s.exec("CREATE VIEW own_counted_external AS SELECT id FROM own_counted")
					s.exec("CREATE TABLE own_counted_sentinel(id INT)")
				})
				s.run(fixture.Exec("INSERT INTO own_counted_sentinel VALUES(9)", 1))
				s.actor("")
				var tableID, indexID, viewID, sequenceID int64
				if err := s.c.QueryRowContext(s.p.Ctx, "SELECT 'own_counted'::regclass::oid::BIGINT,'own_counted_index'::regclass::oid::BIGINT,'own_counted_external'::regclass::oid::BIGINT").Scan(&tableID, &indexID, &viewID); err != nil || tableID <= 0 || indexID <= 0 || viewID <= 0 {
					s.t.Fatal("deletion group identities", tableID, indexID, viewID, err)
				}
				if serial {
					if err := s.c.QueryRowContext(s.p.Ctx, "SELECT 'own_counted_id_seq'::regclass::oid::BIGINT").Scan(&sequenceID); err != nil || sequenceID <= 0 {
						s.t.Fatal("owned sequence identity", sequenceID, err)
					}
				}
				// SERIAL's actual sequence has its own shared owner dependency and
				// collected deletion address. Table rowtypes/indexes are associated
				// identities, not extra owner addresses in this fixture.
				message := "cannot drop table own_counted because other objects depend on it"
				if serial {
					message = "cannot drop desired object(s) because other objects depend on them"
				}
				s.clearNotices()
				ownershipFailure(s, "DROP OWNED BY own_source RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: message, Detail: "view own_counted_external depends on table own_counted", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
				ownershipNotices(s)
				ownershipOwner(s, "own_counted", "own_source")
				ownershipOwner(s, "own_counted_index", "own_source")
				ownershipOwner(s, "own_counted_external", "own_other")
				ownershipSelect(s, "SELECT 'own_counted'::regclass::oid::BIGINT AS table_oid,'own_counted_index'::regclass::oid::BIGINT AS index_oid,'own_counted_external'::regclass::oid::BIGINT AS view_oid", []string{"table_oid", "index_oid", "view_oid"}, []string{"INT8", "INT8", "INT8"}, [][]any{{tableID, indexID, viewID}})
				ownershipSelect(s, "SELECT id,payload FROM own_counted", []string{"id", "payload"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(9)}})
				ownershipRows(s, "own_counted_external")
				s.actor("own_reader")
				ownershipRows(s, "own_counted") // Received grant survives refusal.
				s.actor("own_other")
				ownershipRows(s, "own_counted")
				s.actor("own_source")
				if serial {
					ownershipOwner(s, "own_counted_id_seq", "own_source")
					ownershipSelect(s, "SELECT 'own_counted_id_seq'::regclass::oid::BIGINT AS sequence_oid", []string{"sequence_oid"}, []string{"INT8"}, [][]any{{sequenceID}})
					ownershipSelect(s, "SELECT currval('own_counted_id_seq') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}})
					ownershipSelect(s, "SELECT nextval('own_counted_id_seq') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}})
				}
				s.actor("")
				s.clearNotices()
				s.exec("DROP OWNED BY own_source CASCADE")
				ownershipNotices(s, ownershipCascadeNotice("drop cascades to view own_counted_external", ""))
				ownershipCount(s, "SELECT count(*) FROM pg_class WHERE relname='own_counted' OR relname='own_counted_index' OR relname='own_counted_id_seq' OR relname='own_counted_external'", 0)
				ownershipSelect(s, "SELECT id FROM own_counted_sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}})
				ownershipOwner(s, "own_counted_sentinel", "own_other")
				s.exec("DROP ROLE own_source")
				ownershipBool(s, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='own_source')", false)
			})
		})
	}
}
