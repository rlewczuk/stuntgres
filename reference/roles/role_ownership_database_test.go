package reference_test

import (
	"github.com/lib/pq"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoleOwnershipDatabase(t *testing.T) {
	ownershipProviderCases(t, func(s *membershipScenario, database string) {
		prefix := "ALTER DATABASE " + pq.QuoteIdentifier(database) + " OWNER TO "
		s.exec("ALTER ROLE own_source CREATEDB")
		s.exec("GRANT own_target TO own_source WITH INHERIT TRUE, SET TRUE")
		s.exec(prefix + "own_source")
		ownershipDatabaseOwner(s, "own_source")
		// Same-owner bypass is database-specific, not table's owner-first rule.
		s.actor("own_reader")
		s.exec(prefix + "own_source")
		ownershipFailure(s, prefix+"own_target", fixture.Diagnostic{Code: "42501", Message: "must be owner of database " + database})
		ownershipFailure(s, "ALTER DATABASE own_missing_database OWNER TO own_absent", fixture.Diagnostic{Code: "42704", Message: `role "own_absent" does not exist`})
		ownershipFailure(s, "ALTER DATABASE own_missing_database OWNER TO own_target", fixture.Diagnostic{Code: "3D000", Message: `database "own_missing_database" does not exist`})
		s.actor("")
		s.exec("REVOKE SET OPTION FOR own_target FROM own_source")
		s.actor("own_source")
		ownershipFailure(s, prefix+"own_target", fixture.Diagnostic{Code: "42501", Message: `must be able to SET ROLE "own_target"`})
		s.actor("")
		s.exec("GRANT own_target TO own_source WITH SET TRUE")
		s.actor("own_source")
		s.exec("GRANT CREATE ON DATABASE " + pq.QuoteIdentifier(database) + " TO own_reader WITH GRANT OPTION")
		s.exec("GRANT CREATE ON DATABASE " + pq.QuoteIdentifier(database) + " TO own_target")
		s.exec("GRANT CREATE ON DATABASE " + pq.QuoteIdentifier(database) + " TO PUBLIC")
		defer func() {
			s.actor("")
			s.exec("REVOKE CREATE ON DATABASE " + pq.QuoteIdentifier(database) + " FROM PUBLIC")
		}()
		s.exec(prefix + "own_target") // successor is deliberately NOCREATEDB/NOLOGIN
		s.actor("")
		ownershipDatabaseOwner(s, "own_target")
		ownershipInherited(s, "own_source", "pg_database_owner", "USAGE", true) // still inherits the new owner
		if s.backend.PG {
			ownershipBool(s, "SELECT has_database_privilege('own_reader',current_database(),'CREATE WITH GRANT OPTION')", true)
			ownershipBool(s, "SELECT has_database_privilege('own_target',current_database(),'CREATE')", true)
			ownershipCount(s, "SELECT count(*) FROM pg_database d CROSS JOIN LATERAL aclexplode(d.datacl) a JOIN pg_roles r ON r.oid=a.grantor WHERE d.datname=current_database() AND r.rolname='own_source'", 0)
		}
		s.actor("own_reader")
		s.exec("GRANT CREATE ON DATABASE " + pq.QuoteIdentifier(database) + " TO own_downstream")
		s.actor("")
		s.exec("REVOKE own_target FROM own_source")
		ownershipInherited(s, "own_source", "pg_database_owner", "USAGE", false)
		s.exec("GRANT own_source TO own_target WITH INHERIT FALSE, SET TRUE")
		s.actor("own_target")
		ownershipFailure(s, prefix+"own_source", fixture.Diagnostic{Code: "42501", Message: "permission denied to change owner of database"})
		s.actor("")
		s.exec("GRANT own_target TO own_actor WITH INHERIT TRUE, SET TRUE")
		s.exec("GRANT own_source TO own_actor WITH INHERIT TRUE, SET TRUE")
		s.actor("own_actor") // inherited CREATEDB does not become an actor attribute
		ownershipFailure(s, prefix+"own_source", fixture.Diagnostic{Code: "42501", Message: "permission denied to change owner of database"})
		s.actor("")
		s.exec("ALTER ROLE own_actor CREATEDB")
		s.actor("own_actor")
		s.exec(prefix + "own_source")
		s.actor("")
		ownershipDatabaseOwner(s, "own_source")
		// Healthy utility Prepare neither resolves the missing role nor changes owner.
		s.exec(`DROP ROLE "own Quoted"`)
		held, err := s.c.PrepareContext(s.p.Ctx, prefix+`"own Quoted"`)
		if err != nil {
			s.t.Fatal("effect-free DB utility Prepare", err)
		}
		defer held.Close()
		ownershipDatabaseOwner(s, "own_source")
		failed, err := held.ExecContext(s.p.Ctx)
		if failed != nil {
			s.t.Fatal("failed held DB utility published result", failed)
		}
		fixture.AssertDiagnostic(s.t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42704", Message: `role "own Quoted" does not exist`})
		s.exec(`CREATE ROLE "own Quoted" NOLOGIN`)
		result, err := held.ExecContext(s.p.Ctx)
		if err != nil {
			s.t.Fatal(err)
		}
		if n, err := result.RowsAffected(); err != nil || n != 0 {
			s.t.Fatal(n, err)
		}
		ownershipDatabaseOwner(s, "own Quoted")
		s.exec(prefix + "own_source")
		s.exec("BEGIN")
		if s.backend.PG {
			s.exec(prefix + "own_target")
			ownershipDatabaseOwner(s, "own_target")
		} else {
			predefinedFailure(s, prefix+"own_target", fixture.Diagnostic{Code: "0A000", Message: "statement is unsupported in an explicit transaction"}, s.prepared, func() string {
				if s.prepared {
					return "prepare"
				}
				return "execute"
			}())
		}
		s.exec("ROLLBACK")
		ownershipDatabaseOwner(s, "own_source")
		if s.backend.PG {
			s.exec("BEGIN")
			s.exec(prefix + "own_target")
			s.exec("COMMIT")
			ownershipDatabaseOwner(s, "own_target")
			s.exec(prefix + "own_source")
		}
		s.exec("BEGIN READ ONLY")
		if !s.backend.PG && s.prepared {
			predefinedFailure(s, prefix+"own_target", fixture.Diagnostic{Code: "0A000", Message: "statement is unsupported in an explicit transaction"}, true, "prepare")
		} else {
			ownershipFailure(s, prefix+"own_target", fixture.Diagnostic{Code: "25006", Message: "cannot execute ALTER DATABASE in a read-only transaction"})
		}
		ownershipAborted(s, prefix+"own_target")
		s.exec("ROLLBACK")
		ownershipDatabaseOwner(s, "own_source")
	})
}
