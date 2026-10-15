package reference_test

import (
	"github.com/lib/pq"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoleOwnershipSharedDatabase(t *testing.T) {
	ownershipProviderCases(t, func(s *membershipScenario, database string) {
		prefix := "ALTER DATABASE " + pq.QuoteIdentifier(database) + " OWNER TO "
		ownershipSeed(s, "own_shared_t", "own_source")
		s.exec(prefix + "own_source")
		s.exec("GRANT own_source TO own_actor WITH INHERIT TRUE, SET FALSE")
		s.exec("GRANT own_target TO own_actor WITH INHERIT TRUE, SET FALSE")
		ownershipDatabaseOwner(s, "own_source")
		ownershipInherited(s, "own_target", "pg_database_owner", "USAGE", false)
		s.actor("own_actor") // NOCREATEDB; generic bulk handler must not require it
		ownershipFailure(s, "REASSIGN OWNED BY own_source TO own_target", fixture.Diagnostic{Code: "42501", Message: `must be able to SET ROLE "own_target"`})
		s.actor("")
		ownershipOwner(s, "own_shared_t", "own_source")
		ownershipRows(s, "own_shared_t")
		ownershipDatabaseOwner(s, "own_source")
		s.exec("GRANT own_target TO own_actor WITH SET TRUE")
		s.actor("own_actor")
		s.exec("REASSIGN OWNED BY own_source,own_source TO own_target")
		s.actor("")
		ownershipOwner(s, "own_shared_t", "own_target")
		ownershipRows(s, "own_shared_t")
		ownershipDatabaseOwner(s, "own_target")
		// No stored membership is invented for the implicit live owner hop.
		s.flags("pg_database_owner", "own_target", s.bootstrap, 0, false, false, false)
		ownershipBool(s, "SELECT pg_has_role('own_target','pg_database_owner','MEMBER WITH ADMIN OPTION')", false)
		ownershipInherited(s, "own_source", "pg_database_owner", "MEMBER", false)
		s.actor("own_source")
		ownershipFailure(s, "SET ROLE pg_database_owner", fixture.Diagnostic{Code: "42501", Message: `permission denied to set role "pg_database_owner"`})
		s.actor("")
		s.exec("CREATE SCHEMA own_db_lost AUTHORIZATION own_source")
		s.exec("GRANT own_other TO own_source WITH INHERIT FALSE, SET TRUE")
		s.actor("own_source")
		ownershipFailure(s, "ALTER SCHEMA own_db_lost OWNER TO own_other", fixture.Diagnostic{Code: "42501", Message: "permission denied for database " + ownershipDatabaseDescription(s, database)})
		s.actor("")
		s.exec("GRANT CREATE ON DATABASE " + pq.QuoteIdentifier(database) + " TO own_source")
		if s.backend.PG {
			ownershipBool(s, "SELECT has_database_privilege('own_other',current_database(),'CREATE')", false)
		}
		s.actor("own_source")
		s.exec("ALTER SCHEMA own_db_lost OWNER TO own_other")
		s.actor("")
		s.exec("DROP OWNED BY own_target RESTRICT")
		ownershipCount(s, "SELECT count(*) FROM pg_class WHERE relname='own_shared_t'", 0)
		ownershipSelect(s, "SELECT current_database() AS name", []string{"name"}, []string{"NAME"}, [][]any{{database}})
		ownershipDatabaseOwner(s, "own_target")
		ownershipRoleDependency(s, "own_target", "owner of database "+ownershipDatabaseDescription(s, database))
		// Only an actual owner transfer clears the shared owner blocker.
		s.exec(prefix + pq.QuoteIdentifier(s.bootstrap))
		s.exec("DROP ROLE own_target")
		ownershipCount(s, "SELECT count(*) FROM pg_roles WHERE rolname='own_target'", 0)
		ownershipDatabaseOwner(s, s.bootstrap)
		ownershipCount(s, "SELECT count(*) FROM pg_roles WHERE rolname='own_source'", 1)
	})
}
