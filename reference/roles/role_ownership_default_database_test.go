package reference_test

import (
	"context"
	"github.com/lib/pq"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceRoleOwnershipDefaultDatabaseAuthority(t *testing.T) {
	ownershipProviderCases(t, func(s *fixture.RoleMembershipScenario, database string) {
		// Registered before mutation; later handle defers run before these phases.
		// Keep ordinary creator deletion ahead of the provider's recipient union
		// even when a body assertion terminates the fixture.
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			for _, q := range []string{
				"ROLLBACK",
				"RESET SESSION AUTHORIZATION",
				"ALTER DATABASE " + pq.QuoteIdentifier(database) + " OWNER TO " + pq.QuoteIdentifier(s.Bootstrap),
				"DROP OWNED BY own_source CASCADE",
				"DROP OWNED BY own_target CASCADE",
			} {
				result, err := s.C.ExecContext(ctx, q)
				if err != nil {
					s.T.Error(q, err)
					continue
				}
				if n, err := result.RowsAffected(); err != nil || n != 0 {
					s.T.Error(q, "affected rows", n, err)
				}
			}
		}()
		s.Exec("ALTER DATABASE " + pq.QuoteIdentifier(database) + " OWNER TO own_source")
		ownershipDefault(s, "own_source", "", "GRANT SELECT ON TABLES TO own_actor")
		ownershipDefault(s, "own_target", "", "GRANT SELECT ON TABLES TO own_alt")
		q := "ALTER DEFAULT PRIVILEGES FOR ROLE pg_database_owner GRANT SELECT ON TABLES TO own_reader"
		s.Actor("own_source")
		held := predefinedHeld(s, q)
		defer held.Statement.Close()
		fixture.RunStep(s.T, s.P.Ctx, held, s.Record, fixture.Exec(q, 0))
		s.Actor("own_target")
		ownershipFailure(s, q, fixture.Diagnostic{Code: "42501", Message: "permission denied to change default privileges"})
		s.Actor("own_source")
		// Actual creator is own_source, not its inherited pg_database_owner role.
		fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "own_source", func() { s.Exec("CREATE TABLE own_actual_creator(id INT)") })
		s.Run(fixture.Exec("INSERT INTO own_actual_creator VALUES(7)", 1))
		s.Exec("SET ROLE pg_database_owner")
		s.Exec("ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO own_source")
		fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "pg_database_owner", func() { s.Exec("CREATE TABLE own_implicit_creator(id INT)") })
		s.Run(fixture.Exec("INSERT INTO own_implicit_creator VALUES(7)", 1))
		s.Exec("RESET ROLE")
		s.Actor("own_reader")
		ownershipDefaultDenied(s, "own_actual_creator")
		ownershipRows(s, "own_implicit_creator")
		s.Actor("own_actor")
		ownershipRows(s, "own_actual_creator")
		s.Actor("")
		peer, err := s.Backend.DB.Conn(s.P.Ctx)
		if err != nil {
			s.T.Fatal(err)
		}
		defer peer.Close()
		fixture.RunStep(s.T, s.P.Ctx, peer, s.Record, fixture.Exec("SET SESSION AUTHORIZATION own_source", 0))
		projection := "SELECT " + dpPrivilegeFields + " FROM information_schema.table_privileges WHERE table_schema='public' AND table_name='own_implicit_creator' AND (grantee='own_source' OR grantee='own_reader')" + dpPrivilegeOrder
		before := fixture.Select(projection, dpPrivilegeColumns, dpPrivilegeTypes, [][]any{{"pg_database_owner", "own_reader", database, "public", "own_implicit_creator", "SELECT", "NO", "YES"}, {"pg_database_owner", "own_source", database, "public", "own_implicit_creator", "SELECT", "YES", "YES"}})
		before.Prepared = s.Prepared
		fixture.RunStep(s.T, s.P.Ctx, peer, s.Record, before)
		statement, err := peer.PrepareContext(s.P.Ctx, projection)
		if err != nil {
			s.T.Fatal(err)
		}
		defer statement.Close()
		old, err := statement.QueryContext(s.P.Ctx)
		if err != nil {
			s.T.Fatal(err)
		}
		defer old.Close()
		s.Exec("ALTER DATABASE " + pq.QuoteIdentifier(database) + " OWNER TO own_target")
		// Returned rows preserve the old visibility/grantability bag; fresh execution
		// uses the new canonical DB owner even though role membership rows did not change.
		fixture.DeleteReferenceConsume(s.T, s.Backend.PG, old, nil, before)
		after := fixture.Select(projection, dpPrivilegeColumns, dpPrivilegeTypes, [][]any{{"pg_database_owner", "own_source", database, "public", "own_implicit_creator", "SELECT", "NO", "YES"}})
		fixture.RunStep(s.T, s.P.Ctx, fixture.HeldStatement{Executor: peer, Statement: statement, SQL: projection}, s.Record, after)
		s.Actor("own_source")
		failed := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied to change default privileges"))
		failed.ErrorStage = "execute"
		fixture.RunStep(s.T, s.P.Ctx, held, s.Record, failed)
		s.Actor("own_target")
		fixture.RunStep(s.T, s.P.Ctx, held, s.Record, fixture.Exec(q, 0))
		fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "own_target", func() { s.Exec("CREATE TABLE own_successor_creator(id INT)") })
		s.Run(fixture.Exec("INSERT INTO own_successor_creator VALUES(7)", 1))
		s.Exec("SET ROLE pg_database_owner")
		fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "pg_database_owner", func() { s.Exec("CREATE TABLE own_new_implicit(id INT)") })
		s.Run(fixture.Exec("INSERT INTO own_new_implicit VALUES(7)", 1))
		s.Exec("RESET ROLE")
		s.Actor("own_reader")
		ownershipDefaultDenied(s, "own_successor_creator")
		ownershipRows(s, "own_new_implicit")
		s.Actor("own_alt")
		ownershipRows(s, "own_successor_creator")
		s.Actor("")
		// Restore attachment identity before ordinary role/default recipient cleanup.
		// The pg_database_owner policy is cleaned by DROP OWNED of its real recipients,
		// never by a protected-role bulk sweep or a manual policy-reversal workaround.
		s.Exec("ALTER DATABASE " + pq.QuoteIdentifier(database) + " OWNER TO " + pq.QuoteIdentifier(s.Bootstrap))
		if err := old.Close(); err != nil {
			s.T.Fatal(err)
		}
		if err := statement.Close(); err != nil {
			s.T.Fatal(err)
		}
		if err := peer.Close(); err != nil {
			s.T.Fatal(err)
		}
		if err := held.Statement.Close(); err != nil {
			s.T.Fatal(err)
		}
		// Delete each ordinary creator's roots before the shared recipient sweep.
		// The remaining pg_database_owner policy has only received-ACL cleanup.
		s.ClearNotices()
		s.Exec("DROP OWNED BY own_source CASCADE")
		ownershipNotices(s)
		s.ClearNotices()
		s.Exec("DROP OWNED BY own_target CASCADE")
		ownershipNotices(s)
	})
}
