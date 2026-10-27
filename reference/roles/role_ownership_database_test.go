package reference_test

import (
	"github.com/lib/pq"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoleOwnershipDatabase(t *testing.T) {
	ownershipProviderCases(t, func(s *fixture.RoleMembershipScenario, database string) {
		fixture.SchemaReferenceCleanup(s.T, "own_db_transaction_probe")
		prefix := "ALTER DATABASE " + pq.QuoteIdentifier(database) + " OWNER TO "
		s.Exec("ALTER ROLE own_source CREATEDB")
		s.Exec("GRANT own_target TO own_source WITH INHERIT TRUE, SET TRUE")
		s.Exec(prefix + "own_source")
		ownershipDatabaseOwner(s, "own_source")
		// Same-owner bypass is database-specific, not table's owner-first rule.
		s.Actor("own_reader")
		s.Exec(prefix + "own_source")
		ownershipFailure(s, prefix+"own_target", fixture.Diagnostic{Code: "42501", Message: "must be owner of database " + database})
		ownershipFailure(s, "ALTER DATABASE own_missing_database OWNER TO own_absent", fixture.Diagnostic{Code: "42704", Message: `role "own_absent" does not exist`})
		ownershipFailure(s, "ALTER DATABASE own_missing_database OWNER TO own_target", fixture.Diagnostic{Code: "3D000", Message: `database "own_missing_database" does not exist`})
		s.Actor("")
		s.Exec("REVOKE SET OPTION FOR own_target FROM own_source")
		s.Actor("own_source")
		ownershipFailure(s, prefix+"own_target", fixture.Diagnostic{Code: "42501", Message: `must be able to SET ROLE "own_target"`})
		s.Actor("")
		s.Exec("GRANT own_target TO own_source WITH SET TRUE")
		s.Actor("own_source")
		s.Exec("GRANT CREATE ON DATABASE " + pq.QuoteIdentifier(database) + " TO own_reader WITH GRANT OPTION")
		s.Exec("GRANT CREATE ON DATABASE " + pq.QuoteIdentifier(database) + " TO own_target")
		s.Exec("GRANT CREATE ON DATABASE " + pq.QuoteIdentifier(database) + " TO PUBLIC")
		defer func() {
			s.Actor("")
			s.Exec("REVOKE CREATE ON DATABASE " + pq.QuoteIdentifier(database) + " FROM PUBLIC")
		}()
		s.Exec(prefix + "own_target") // successor is deliberately NOCREATEDB/NOLOGIN
		s.Actor("")
		ownershipDatabaseOwner(s, "own_target")
		ownershipInherited(s, "own_source", "pg_database_owner", "USAGE", true) // still inherits the new owner
		if s.Backend.PG {
			ownershipBool(s, "SELECT has_database_privilege('own_reader',current_database(),'CREATE WITH GRANT OPTION')", true)
			ownershipBool(s, "SELECT has_database_privilege('own_target',current_database(),'CREATE')", true)
			ownershipCount(s, "SELECT count(*) FROM pg_database d CROSS JOIN LATERAL aclexplode(d.datacl) a JOIN pg_roles r ON r.oid=a.grantor WHERE d.datname=current_database() AND r.rolname='own_source'", 0)
		}
		s.Actor("own_reader")
		s.Exec("GRANT CREATE ON DATABASE " + pq.QuoteIdentifier(database) + " TO own_downstream")
		s.Actor("")
		s.Exec("REVOKE own_target FROM own_source")
		ownershipInherited(s, "own_source", "pg_database_owner", "USAGE", false)
		s.Exec("GRANT own_source TO own_target WITH INHERIT FALSE, SET TRUE")
		s.Actor("own_target")
		ownershipFailure(s, prefix+"own_source", fixture.Diagnostic{Code: "42501", Message: "permission denied to change owner of database"})
		s.Actor("")
		s.Exec("GRANT own_target TO own_actor WITH INHERIT TRUE, SET TRUE")
		s.Exec("GRANT own_source TO own_actor WITH INHERIT TRUE, SET TRUE")
		s.Actor("own_actor") // inherited CREATEDB does not become an actor attribute
		ownershipFailure(s, prefix+"own_source", fixture.Diagnostic{Code: "42501", Message: "permission denied to change owner of database"})
		s.Actor("")
		s.Exec("ALTER ROLE own_actor CREATEDB")
		s.Actor("own_actor")
		s.Exec(prefix + "own_source")
		s.Actor("")
		ownershipDatabaseOwner(s, "own_source")
		// Healthy utility Prepare neither resolves the missing role nor changes owner.
		s.Exec(`DROP ROLE "own Quoted"`)
		held, err := s.C.PrepareContext(s.P.Ctx, prefix+`"own Quoted"`)
		if err != nil {
			s.T.Fatal("effect-free DB utility Prepare", err)
		}
		defer held.Close()
		ownershipDatabaseOwner(s, "own_source")
		failed, err := held.ExecContext(s.P.Ctx)
		if failed != nil {
			s.T.Fatal("failed held DB utility published result", failed)
		}
		fixture.AssertDiagnostic(s.T, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42704", Message: `role "own Quoted" does not exist`})
		s.Exec(`CREATE ROLE "own Quoted" NOLOGIN`)
		result, err := held.ExecContext(s.P.Ctx)
		if err != nil {
			s.T.Fatal(err)
		}
		if n, err := result.RowsAffected(); err != nil || n != 0 {
			s.T.Fatal(n, err)
		}
		ownershipDatabaseOwner(s, "own Quoted")
		s.Exec(prefix + "own_source")
		// Public schema ACLs are fixture-owned. Use the real implicit owner of
		// a dedicated namespace instead of granting the actor blanket access.
		s.Exec("CREATE SCHEMA own_db_transaction_probe AUTHORIZATION pg_database_owner")
		s.Actor("own_actor") // Existing ordinary CREATEDB actor has SET/INHERIT on both owners.
		s.Exec("BEGIN")
		s.Exec(prefix + "own_target")
		ownershipDatabaseOwnerTransactional(s, "own_target")
		s.Exec("ROLLBACK")
		s.Actor("")
		ownershipDatabaseOwner(s, "own_source")
		s.Exec("BEGIN")
		s.Exec(prefix + "own_target")
		s.Exec("COMMIT")
		ownershipDatabaseOwner(s, "own_target")
		s.Exec(prefix + "own_source")
		s.Exec("BEGIN READ ONLY")
		ownershipFailure(s, prefix+"own_target", fixture.Diagnostic{Code: "25006", Message: "cannot execute ALTER DATABASE in a read-only transaction"})
		ownershipAborted(s, prefix+"own_target")
		s.Exec("ROLLBACK")
		ownershipDatabaseOwner(s, "own_source")
	})
}

// The ordinary session actor is selected before BEGIN. Inside the block only
// transaction-safe SET ROLE is used; no new session-authorization admission.
func ownershipDatabaseOwnerTransactional(s *fixture.RoleMembershipScenario, owner string) {
	s.T.Helper()
	ownershipSelect(s, "SELECT r.rolname FROM pg_database d JOIN pg_roles r ON r.oid=d.datdba WHERE d.datname=current_database()", []string{"rolname"}, []string{"NAME"}, [][]any{{owner}})
	ownershipBool(s, "SELECT pg_has_role("+pq.QuoteLiteral(owner)+",'pg_database_owner','USAGE')", true)
	s.Exec("RESET ROLE")
	s.Exec("CREATE TABLE own_db_transaction_probe.own_inheritance_probe(id INT)")
	s.Run(fixture.Exec("INSERT INTO own_db_transaction_probe.own_inheritance_probe VALUES(7)", 1))
	s.Exec("ALTER TABLE own_db_transaction_probe.own_inheritance_probe OWNER TO pg_database_owner")
	s.Exec("SET ROLE " + pq.QuoteIdentifier(owner))
	ownershipRows(s, "own_db_transaction_probe.own_inheritance_probe")
	s.Exec("RESET ROLE")
	s.Exec("DROP TABLE own_db_transaction_probe.own_inheritance_probe")
	s.Exec("SET ROLE " + pq.QuoteIdentifier(owner))
	s.Exec("SET ROLE pg_database_owner")
	ownershipSelect(s, "SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{"pg_database_owner"}})
	s.Exec("RESET ROLE")
}
