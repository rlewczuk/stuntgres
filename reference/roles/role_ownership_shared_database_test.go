package reference_test

import (
	"github.com/lib/pq"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoleOwnershipSharedDatabase(t *testing.T) {
	ownershipProviderCases(t, func(s *fixture.RoleMembershipScenario, database string) {
		prefix := "ALTER DATABASE " + pq.QuoteIdentifier(database) + " OWNER TO "
		ownershipSeed(s, "own_shared_t", "own_source")
		s.Exec(prefix + "own_source")
		s.Exec("GRANT own_source TO own_actor WITH INHERIT TRUE, SET FALSE")
		s.Exec("GRANT own_target TO own_actor WITH INHERIT TRUE, SET FALSE")
		ownershipDatabaseOwner(s, "own_source")
		ownershipInherited(s, "own_target", "pg_database_owner", "USAGE", false)
		s.Actor("own_actor") // NOCREATEDB; generic bulk handler must not require it
		ownershipFailure(s, "REASSIGN OWNED BY own_source TO own_target", fixture.Diagnostic{Code: "42501", Message: `must be able to SET ROLE "own_target"`})
		s.Actor("")
		ownershipOwner(s, "own_shared_t", "own_source")
		ownershipRows(s, "own_shared_t")
		ownershipDatabaseOwner(s, "own_source")
		s.Exec("GRANT own_target TO own_actor WITH SET TRUE")
		s.Actor("own_actor")
		s.Exec("REASSIGN OWNED BY own_source,own_source TO own_target")
		s.Actor("")
		ownershipOwner(s, "own_shared_t", "own_target")
		ownershipRows(s, "own_shared_t")
		ownershipDatabaseOwner(s, "own_target")
		// No stored membership is invented for the implicit live owner hop.
		s.Flags("pg_database_owner", "own_target", s.Bootstrap, 0, false, false, false)
		ownershipBool(s, "SELECT pg_has_role('own_target','pg_database_owner','MEMBER WITH ADMIN OPTION')", false)
		ownershipInherited(s, "own_source", "pg_database_owner", "MEMBER", false)
		s.Actor("own_source")
		ownershipFailure(s, "SET ROLE pg_database_owner", fixture.Diagnostic{Code: "42501", Message: `permission denied to set role "pg_database_owner"`})
		s.Actor("")
		s.Exec("CREATE SCHEMA own_db_lost AUTHORIZATION own_source")
		s.Exec("GRANT own_other TO own_source WITH INHERIT FALSE, SET TRUE")
		s.Actor("own_source")
		ownershipFailure(s, "ALTER SCHEMA own_db_lost OWNER TO own_other", fixture.Diagnostic{Code: "42501", Message: "permission denied for database " + ownershipDatabaseDescription(s, database)})
		s.Actor("")
		s.Exec("GRANT CREATE ON DATABASE " + pq.QuoteIdentifier(database) + " TO own_source")
		if s.Backend.PG {
			ownershipBool(s, "SELECT has_database_privilege('own_other',current_database(),'CREATE')", false)
		}
		s.Actor("own_source")
		s.Exec("ALTER SCHEMA own_db_lost OWNER TO own_other")
		s.Actor("")
		s.Exec("DROP OWNED BY own_target RESTRICT")
		ownershipCount(s, "SELECT count(*) FROM pg_class WHERE relname='own_shared_t'", 0)
		ownershipSelect(s, "SELECT current_database() AS name", []string{"name"}, []string{"NAME"}, [][]any{{database}})
		ownershipDatabaseOwner(s, "own_target")
		ownershipRoleDependency(s, "own_target", "owner of database "+ownershipDatabaseDescription(s, database))
		// Only an actual owner transfer clears the shared owner blocker.
		s.Exec(prefix + pq.QuoteIdentifier(s.Bootstrap))
		s.Exec("DROP ROLE own_target")
		ownershipCount(s, "SELECT count(*) FROM pg_roles WHERE rolname='own_target'", 0)
		ownershipDatabaseOwner(s, s.Bootstrap)
		ownershipCount(s, "SELECT count(*) FROM pg_roles WHERE rolname='own_source'", 1)
	})
}
