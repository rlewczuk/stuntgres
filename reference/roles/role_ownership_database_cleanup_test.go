package reference_test

import (
	"github.com/lib/pq"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoleOwnershipDatabaseCleanup(t *testing.T) {
	ownershipProviderCases(t, func(s *fixture.RoleMembershipScenario, database string) {
		db := pq.QuoteIdentifier(database)
		for _, q := range []string{"GRANT CREATE ON DATABASE " + db + " TO own_source WITH GRANT OPTION", "GRANT CREATE ON DATABASE " + db + " TO own_alt WITH GRANT OPTION", "GRANT CREATE ON DATABASE " + db + " TO own_reader"} {
			s.Exec(q)
		}
		s.Actor("own_source")
		s.Exec("GRANT CREATE ON DATABASE " + db + " TO own_downstream WITH GRANT OPTION")
		s.Exec("GRANT CREATE ON DATABASE " + db + " TO own_actor WITH GRANT OPTION")
		s.Actor("own_downstream")
		s.Exec("GRANT CREATE ON DATABASE " + db + " TO own_reader")
		s.Actor("own_actor")
		s.Exec("GRANT CREATE ON DATABASE " + db + " TO own_middle")
		s.Actor("own_alt")
		s.Exec("GRANT CREATE ON DATABASE " + db + " TO own_downstream")
		s.Actor("")
		ownershipSeed(s, "own_db_moved", "own_source")
		s.Exec("REASSIGN OWNED BY own_source TO own_target")
		ownershipOwner(s, "own_db_moved", "own_target")
		ownershipRows(s, "own_db_moved")
		ownershipRoleDependency(s, "own_source", "privileges for database "+ownershipDatabaseDescription(s, database))
		ownershipSeed(s, "own_db_blocked", "own_source")
		s.Exec("CREATE VIEW own_db_external AS SELECT id FROM own_db_blocked")
		ownershipFailure(s, "DROP OWNED BY own_source RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop table own_db_blocked because other objects depend on it", Detail: "view own_db_external depends on table own_db_blocked", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
		ownershipRows(s, "own_db_blocked")
		ownershipRows(s, "own_db_external")
		// The refused statement must not revoke shared CREATE. It authorizes a real
		// trusted install, not a privilege inquiry or fake successful probe.
		s.Actor("own_source")
		fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "own_source", func() { s.Exec("CREATE EXTENSION unaccent") })
		ownershipHotel(s, "SELECT unaccent('Hôtel') AS v")
		s.Actor("")
		ownershipProviderOwners(s, "own_source", s.Bootstrap)
		s.ClearNotices()
		s.Exec("DROP OWNED BY own_source CASCADE")
		ownershipNotices(s, ownershipCascadeNotice("drop cascades to view own_db_external", ""))
		ownershipProviderGone(s)
		ownershipCount(s, "SELECT count(*) FROM pg_class WHERE relname='own_db_blocked' OR relname='own_db_external'", 0)
		ownershipRows(s, "own_db_moved")
		if s.Backend.PG {
			for _, role := range []string{"own_source", "own_actor", "own_middle"} {
				ownershipBool(s, "SELECT has_database_privilege("+pq.QuoteLiteral(role)+",current_database(),'CREATE')", false)
			}
			for _, role := range []string{"own_downstream", "own_reader", "own_alt"} {
				ownershipBool(s, "SELECT has_database_privilege("+pq.QuoteLiteral(role)+",current_database(),'CREATE')", true)
			}
			ownershipBool(s, "SELECT has_database_privilege('own_downstream',current_database(),'CREATE WITH GRANT OPTION')", false)
			ownershipCount(s, "SELECT count(*) FROM pg_database d CROSS JOIN LATERAL aclexplode(d.datacl) a JOIN pg_roles r ON r.oid=a.grantor WHERE d.datname=current_database() AND (r.rolname='own_source' OR r.rolname='own_downstream' OR r.rolname='own_actor')", 0)
		}
		for _, role := range []string{"own_source", "own_actor", "own_middle"} {
			s.Actor(role)
			ownershipFailure(s, "CREATE EXTENSION unaccent", fixture.Diagnostic{Code: "42501", Message: `permission denied to create extension "unaccent"`, Hint: "Must have CREATE privilege on current database to create this extension."})
			s.Actor("")
		}
		s.Actor("own_downstream")
		s.Exec("CREATE EXTENSION unaccent")
		ownershipHotel(s, "SELECT unaccent('Hôtel') AS v")
		s.Actor("")
		s.Exec("DROP OWNED BY own_downstream CASCADE")
		ownershipProviderGone(s)
		s.Actor("own_reader")
		s.Exec("CREATE EXTENSION unaccent")
		ownershipHotel(s, "SELECT unaccent('Hôtel') AS v")
		s.Actor("")
		s.Exec("DROP OWNED BY own_reader CASCADE")
		ownershipProviderGone(s)
		s.Exec("DROP ROLE own_source")
		// PUBLIC is independent use, never a grant-option source. Unrelated cleanup
		// must preserve its actual trusted-install authority.
		s.Exec("GRANT CREATE ON DATABASE " + db + " TO PUBLIC")
		s.Exec("DROP OWNED BY own_actor")
		s.Actor("own_other")
		s.Exec("CREATE EXTENSION unaccent")
		ownershipHotel(s, "SELECT unaccent('Hôtel') AS v")
		s.Actor("")
		s.Exec("REVOKE CREATE ON DATABASE " + db + " FROM PUBLIC")
		ownershipSelect(s, "SELECT current_database() AS name", []string{"name"}, []string{"NAME"}, [][]any{{database}})
	})
}
