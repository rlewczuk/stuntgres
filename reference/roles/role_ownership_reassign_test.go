package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleOwnershipReassignAuthority(t *testing.T) {
	ownershipCases(t, func(s *membershipScenario) {
		ownershipSeed(s, "own_reassign_auth", "own_source")
		s.exec("GRANT own_source TO own_actor WITH INHERIT FALSE, SET TRUE")
		s.exec("GRANT own_target TO own_actor WITH INHERIT TRUE, SET FALSE")
		s.actor("own_actor")
		ownershipFailure(s, "REASSIGN OWNED BY own_source TO own_target", fixture.Diagnostic{Code: "42501", Message: "permission denied to reassign objects", Detail: `Only roles with privileges of role "own_source" may reassign objects owned by it.`})
		s.actor("")
		s.exec("GRANT own_source TO own_actor WITH INHERIT TRUE, SET FALSE")
		s.exec("GRANT own_target TO own_actor WITH INHERIT FALSE, SET TRUE")
		s.actor("own_actor")
		ownershipFailure(s, "REASSIGN OWNED BY own_source TO own_target", fixture.Diagnostic{Code: "42501", Message: "permission denied to reassign objects", Detail: `Only roles with privileges of role "own_target" may reassign objects to it.`})
		s.actor("")
		s.exec("GRANT own_target TO own_actor WITH INHERIT TRUE, SET FALSE")
		s.exec("ALTER ROLE own_target NOLOGIN")
		s.actor("own_actor")
		s.exec("REASSIGN OWNED BY own_source,own_source TO own_target")
		s.actor("")
		ownershipOwner(s, "own_reassign_auth", "own_target")
		ownershipRows(s, "own_reassign_auth")
		s.exec("REASSIGN OWNED BY own_target TO own_source")
		s.exec("REVOKE INHERIT OPTION FOR own_source FROM own_actor")
		s.exec("GRANT own_source TO own_middle WITH INHERIT TRUE, SET FALSE")
		s.exec("GRANT own_middle TO own_actor WITH INHERIT TRUE, SET FALSE")
		s.exec("GRANT own_source TO own_alt WITH INHERIT TRUE, SET FALSE")
		s.exec("GRANT own_alt TO own_actor WITH INHERIT TRUE, SET FALSE")
		s.exec("REVOKE INHERIT OPTION FOR own_middle FROM own_actor")
		s.exec("GRANT own_target TO own_downstream WITH INHERIT TRUE, SET FALSE")
		s.exec("GRANT own_downstream TO own_actor WITH INHERIT TRUE, SET FALSE")
		s.exec("REVOKE INHERIT OPTION FOR own_target FROM own_actor")
		s.actor("own_actor")
		s.exec("REASSIGN OWNED BY own_source TO own_target")
		s.actor("")
		ownershipOwner(s, "own_reassign_auth", "own_target")
		s.exec("REASSIGN OWNED BY own_target TO own_source")
		ownershipFailure(s, "REASSIGN OWNED BY own_source,own_absent TO own_target", fixture.Diagnostic{Code: "42704", Message: `role "own_absent" does not exist`})
		ownershipOwner(s, "own_reassign_auth", "own_source")
		ownershipFailure(s, "REASSIGN OWNED BY own_source TO own_absent", fixture.Diagnostic{Code: "42704", Message: `role "own_absent" does not exist`})
		// PG fixture postgres is a user-created SU role, not initdb's stuntgres.
		// Exercise its observed success with a real root, never bootstrap/shared DB.
		if s.backend.PG {
			ownershipSeed(s, "own_postgres_reassign", "postgres")
			s.exec("REASSIGN OWNED BY postgres TO own_target")
			ownershipOwner(s, "own_postgres_reassign", "own_target")
			ownershipRows(s, "own_postgres_reassign")
			ownershipOwner(s, "own_reassign_auth", "own_source")
		}
		ownershipSelect(s, "SELECT oid::BIGINT AS oid FROM pg_roles WHERE rolname='pg_database_owner'", []string{"oid"}, []string{"INT8"}, [][]any{{int64(6171)}})
		ownershipFailure(s, "REASSIGN OWNED BY pg_database_owner TO own_target", fixture.Diagnostic{Code: "2BP01", Message: "cannot reassign ownership of objects owned by role pg_database_owner because they are required by the database system"})
		s.actor("own_source")
		ownershipSelect(s, "SELECT current_user,session_user", []string{"current_user", "session_user"}, []string{"NAME", "NAME"}, [][]any{{"own_source", "own_source"}})
		s.exec("REASSIGN OWNED BY CURRENT_USER,CURRENT_ROLE,SESSION_USER TO own_source")
		for _, target := range []string{"CURRENT_USER", "CURRENT_ROLE", "SESSION_USER"} {
			s.exec("REASSIGN OWNED BY CURRENT_USER,CURRENT_ROLE,SESSION_USER TO " + target)
		}
		s.actor("")
		ownershipOwner(s, "own_reassign_auth", "own_source")
	})
}

func TestReferenceRoleOwnershipReassignFamilies(t *testing.T) {
	ownershipCases(t, func(s *membershipScenario) {
		s.exec("CREATE SCHEMA own_bulk AUTHORIZATION own_source")
		s.actor("own_source")
		for _, q := range []string{
			"CREATE TABLE own_bulk.t(id SERIAL,payload INT)",
			"CREATE INDEX own_bulk_index ON own_bulk.t(payload)",
			"CREATE TABLE own_bulk.p(id INT) PARTITION BY RANGE(id)",
			"CREATE TABLE own_bulk.leaf PARTITION OF own_bulk.p FOR VALUES FROM(0) TO(10)",
			"CREATE VIEW own_bulk.v AS SELECT payload FROM own_bulk.t",
			"CREATE MATERIALIZED VIEW own_bulk.mv AS SELECT 7::INT AS id",
			"CREATE SEQUENCE own_bulk.s",
			"CREATE TYPE own_bulk.e AS ENUM('a')",
			"CREATE DOMAIN own_bulk.d AS INT",
			"CREATE TYPE own_bulk.c AS(id INT)",
			"CREATE COLLATION own_bulk.coll FROM \"C\"",
		} {
			ownershipDefinition(s, q)
		}
		s.run(fixture.Exec("INSERT INTO own_bulk.t(payload) VALUES(7)", 1))
		s.run(fixture.Exec("INSERT INTO own_bulk.p VALUES(7)", 1))
		s.actor("")
		ownershipSeed(s, "own_unrelated", "own_other")
		// Superuser bulk exercises every class without conflating its gates with
		// relation/type bulk INHERIT authority covered by ReassignAuthority.
		s.exec("REASSIGN OWNED BY own_source TO own_target")
		for _, name := range []string{"own_bulk.t", "own_bulk.own_bulk_index", "own_bulk.t_id_seq", "own_bulk.p", "own_bulk.leaf", "own_bulk.v", "own_bulk.mv", "own_bulk.s", "own_bulk.c"} {
			ownershipOwner(s, name, "own_target")
		}
		ownershipOwner(s, "own_unrelated", "own_other")
		ownershipRows(s, "own_unrelated")
		s.exec("GRANT " + s.bootstrap + " TO own_target WITH INHERIT FALSE, SET TRUE")
		ownershipInherited(s, "own_target", s.bootstrap, "USAGE", false)
		s.actor("own_target")
		s.exec("CREATE TABLE own_bulk.authority(id INT)")
		for _, typ := range []struct{ kind, name string }{{"TYPE", "e"}, {"DOMAIN", "d"}, {"TYPE", "c"}} {
			s.exec("ALTER " + typ.kind + " own_bulk." + typ.name + " OWNER TO " + s.bootstrap)
			s.actor("")
			s.exec("ALTER " + typ.kind + " own_bulk." + typ.name + " OWNER TO own_target")
			s.actor("own_target")
		}
		s.exec("ALTER COLLATION own_bulk.coll RENAME TO coll_changed")
		ownershipSelect(s, "SELECT payload FROM own_bulk.t", []string{"payload"}, []string{"INT4"}, [][]any{{int64(7)}})
		ownershipRows(s, "own_bulk.p")
		ownershipRows(s, "own_bulk.mv")
		ownershipSelect(s, "SELECT nextval('own_bulk.s') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}})
		s.actor("")
		ownershipCount(s, "SELECT count(*) FROM pg_type t JOIN pg_type a ON a.oid=t.typarray JOIN pg_roles r ON r.oid=t.typowner JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname='own_bulk' AND t.typname IN('e','d','c') AND a.typowner=t.typowner AND r.rolname='own_target'", 3)
	})
}

func TestReferenceRoleOwnershipReassignPreservation(t *testing.T) {
	ownershipCases(t, func(s *membershipScenario) {
		ownershipSeed(s, "own_preserve", "own_source")
		ownershipSeed(s, "own_other_table", "own_other")
		s.actor("own_source")
		s.exec("GRANT SELECT ON own_preserve TO own_reader")
		s.actor("own_other")
		s.exec("GRANT SELECT ON own_other_table TO own_source")
		s.exec("GRANT UPDATE(id) ON own_other_table TO own_source")
		s.actor("")
		s.exec("REASSIGN OWNED BY own_source TO own_target")
		ownershipOwner(s, "own_preserve", "own_target")
		s.actor("own_reader")
		ownershipRows(s, "own_preserve")
		s.actor("own_source")
		ownershipRows(s, "own_other_table")
		s.run(fixture.Exec("UPDATE own_other_table SET id=7", 1))
		s.actor("")
		ownershipRoleDependency(s, "own_source", "privileges for table own_other_table", "privileges for column id of table own_other_table")
		s.exec("DROP OWNED BY own_source")
		ownershipOwner(s, "own_other_table", "own_other")
		ownershipRows(s, "own_other_table")
		s.exec("DROP ROLE own_source")
		s.actor("own_reader")
		ownershipRows(s, "own_preserve")
		s.actor("")
	})
}

func TestReferenceRoleOwnershipDefaultsBoundary(t *testing.T) {
	ownershipCases(t, func(s *membershipScenario) {
		ownershipDefaultSchemas(s)
		ownershipDefaultTable(s, "own_schema.own_default_existing", "own_source")
		s.exec("GRANT SELECT ON own_schema.own_default_existing TO own_reader")
		lines := ownershipDefaultTen(s, "own_source", "own_schema", "own_reader")
		ids := ownershipDefaultIDs(s, "own_source", 10)
		ownershipDefaultKeyCount(s, "own_source", "", "f", 1)
		ownershipDefaultKeyCount(s, "own_source", "own_schema", "f", 1)
		ownershipDefault(s, "own_target", "", "GRANT SELECT ON TABLES TO own_actor")
		ownershipDefault(s, "own_alt", "own_bulk", "GRANT INSERT ON TABLES TO own_reader")
		s.exec("REASSIGN OWNED BY own_source TO own_target")
		ownershipOwner(s, "own_schema.own_default_existing", "own_target")
		ownershipDefaultStable(s, "own_source", ids)
		ownershipRoleDependency(s, "own_source", lines...)
		ownershipDefaultTable(s, "own_schema.own_default_later", "own_source")
		ownershipDefaultTable(s, "own_schema.own_target_later", "own_target")
		s.actor("own_reader")
		ownershipRows(s, "own_schema.own_default_existing")
		ownershipRows(s, "own_schema.own_default_later")
		ownershipDefaultDenied(s, "own_schema.own_target_later")
		s.actor("own_actor")
		ownershipRows(s, "own_schema.own_target_later")
		ownershipDefaultDenied(s, "own_schema.own_default_later")
		s.actor("")
		s.exec("REASSIGN OWNED BY own_source TO own_target")
		s.actor("own_actor")
		ownershipDefaultDenied(s, "own_schema.own_default_existing")
		ownershipDefaultDenied(s, "own_schema.own_default_later")
		ownershipRows(s, "own_schema.own_target_later")
		s.actor("")
		ownershipRoleDependency(s, "own_source", lines...)
		ownershipDefaultStable(s, "own_source", ids)
		ownershipDefaultKeyCount(s, "own_alt", "own_bulk", "r", 1)
		ownershipDefaultTable(s, "own_bulk.own_unrelated_default", "own_alt")
		s.actor("own_reader")
		s.run(fixture.Exec("INSERT INTO own_bulk.own_unrelated_default VALUES(9)", 1))
		s.actor("")
		s.exec("DROP OWNED BY own_source RESTRICT")
		s.exec("DROP ROLE own_source")
		ownershipOwner(s, "own_schema.own_default_existing", "own_target")
		s.actor("own_reader")
		ownershipRows(s, "own_schema.own_default_existing")
		ownershipRows(s, "own_schema.own_default_later")
		s.actor("")
	})
}
