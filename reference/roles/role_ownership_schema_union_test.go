package reference_test

import (
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleOwnershipRestrictSchemaUnion(t *testing.T) {
	ownershipCases(t, func(s *membershipScenario) {
		s.exec("CREATE SCHEMA own_union_schema AUTHORIZATION own_source")
		s.actor("own_source")
		for _, q := range []string{
			"CREATE TABLE own_union_schema.t(id SERIAL)",
			"CREATE VIEW own_union_schema.v AS SELECT id FROM own_union_schema.t",
			"CREATE MATERIALIZED VIEW own_union_schema.mv AS SELECT 7::INT AS id",
			"CREATE SEQUENCE own_union_schema.s",
			"CREATE TYPE own_union_schema.e AS ENUM('a')",
			"CREATE DOMAIN own_union_schema.d AS INT",
			"CREATE TYPE own_union_schema.c AS(id INT)",
			"CREATE COLLATION own_union_schema.coll FROM \"C\"",
		} {
			ownershipDefinition(s, q)
		}
		s.run(fixture.Exec("INSERT INTO own_union_schema.t VALUES(7)", 1))
		var namespaceID int64
		if err := s.c.QueryRowContext(s.p.Ctx, "SELECT relnamespace FROM pg_class WHERE oid='own_union_schema.t'::regclass::oid").Scan(&namespaceID); err != nil || namespaceID <= 0 {
			s.t.Fatal("owned namespace identity", namespaceID, err)
		}
		// Inherited schema ownership permits external lookup, but root deletion
		// still enumerates exact owners: this view is owned by own_other.
		s.actor("")
		s.exec("GRANT own_source TO own_other WITH INHERIT TRUE, SET FALSE")
		s.exec("GRANT SELECT ON own_union_schema.t TO own_other")
		s.actor("own_other")
		fixture.WithPublicSchemaOwner(s.t, s.p, s.backend, "own_other", func() { s.exec("CREATE VIEW own_union_schema_external AS SELECT id FROM own_union_schema.t") })
		s.actor("")
		s.exec("GRANT own_source TO own_actor WITH INHERIT FALSE, SET TRUE")
		s.actor("own_actor")
		ownershipFailure(s, "DROP OWNED BY own_source", fixture.Diagnostic{Code: "42501", Message: "permission denied to drop objects", Detail: `Only roles with privileges of role "own_source" may drop objects owned by it.`})
		s.actor("")
		s.exec("GRANT own_source TO own_actor WITH INHERIT TRUE, SET FALSE")
		s.actor("own_actor")
		s.clearNotices()
		ownershipFailure(s, "DROP OWNED BY own_source RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop desired object(s) because other objects depend on them", Detail: "view own_union_schema_external depends on table own_union_schema.t", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
		ownershipNotices(s)
		ownershipRows(s, "own_union_schema.t")
		s.actor("own_other")
		ownershipRows(s, "own_union_schema_external")
		s.actor("own_source")
		s.exec("CREATE TABLE own_union_schema.after_refusal(id INT)")
		ownershipSelect(s, "SELECT nextval('own_union_schema.s') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}})
		ownershipSelect(s, "SELECT 'a'::own_union_schema.e::TEXT AS e,7::own_union_schema.d::INT AS d,'(7)'::own_union_schema.c::TEXT AS c", []string{"e", "d", "c"}, []string{"TEXT", "INT4", "TEXT"}, [][]any{{"a", int64(7), "(7)"}})
		s.actor("")
		s.exec("DROP VIEW own_union_schema_external")
		s.actor("own_actor")
		s.clearNotices()
		s.exec("DROP OWNED BY own_source RESTRICT")
		ownershipNotices(s)
		s.actor("")
		ownershipCount(s, "SELECT count(*) FROM pg_class WHERE relnamespace="+strconv.FormatInt(namespaceID, 10), 0)
		if s.backend.PG {
			ownershipCount(s, "SELECT count(*) FROM pg_namespace WHERE nspname='own_union_schema'", 0)
			ownershipCount(s, "SELECT count(*) FROM pg_type WHERE typnamespace="+strconv.FormatInt(namespaceID, 10), 0)
		}
		// Actual schema/type recreation proves cleanup, without adding absent
		// native namespace/type catalog projections just for this test.
		s.exec("CREATE SCHEMA own_union_schema AUTHORIZATION own_source")
		s.actor("own_source")
		s.exec("CREATE TYPE own_union_schema.e AS ENUM('b')")
		s.exec("CREATE TABLE own_union_schema.t(id INT)")
		s.run(fixture.Exec("INSERT INTO own_union_schema.t VALUES(7)", 1))
		ownershipRows(s, "own_union_schema.t")
		s.actor("")
		s.exec("DROP OWNED BY own_source")
		s.exec("DROP ROLE own_source")
	})
}
