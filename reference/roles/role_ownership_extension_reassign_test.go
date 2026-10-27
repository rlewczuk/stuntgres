package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoleOwnershipExtensionReassign(t *testing.T) {
	ownershipProviderCases(t, func(s *fixture.RoleMembershipScenario, database string) {
		ownershipProviderSetup(s, database, false)
		ownershipProviderOwners(s, "own_source", s.Bootstrap)
		initial := ownershipProviderIDs(s)
		s.Actor("own_other")
		fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "own_other", func() {
			s.Exec("CREATE TABLE own_provider_consumer(id INT,v TEXT DEFAULT own_provider.unaccent('Hôtel'),CHECK(own_provider.unaccent(v)<>''))")
		})
		s.Run(fixture.Exec("INSERT INTO own_provider_consumer(id) VALUES(7)", 1))
		fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "own_other", func() {
			s.Exec("CREATE VIEW own_provider_view AS SELECT own_provider.unaccent(v) AS v FROM own_provider_consumer")
		})
		s.Exec("CREATE POLICY own_provider_policy ON own_provider_consumer TO own_reader USING(own_provider.unaccent(v)<>'')")
		s.Actor("")
		s.Exec("GRANT own_other TO own_reader WITH INHERIT TRUE, SET FALSE")
		s.Exec("ALTER TABLE own_provider_consumer ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY")
		s.Actor("own_reader")
		ownershipPolicyRows(s, "SELECT id FROM own_provider_consumer", []string{"id"}, [][]any{{int64(7)}})
		s.Actor("own_other")
		ownershipSelect(s, "SELECT id FROM own_provider_consumer", []string{"id"}, []string{"INT4"}, nil)
		s.Exec("ALTER TABLE own_provider_consumer DISABLE ROW LEVEL SECURITY")
		s.Actor("")
		ownershipHotel(s, "SELECT v FROM own_provider_view")
		held := ownershipProviderHeld(s, "SELECT v FROM own_provider_view")
		defer held.Close()
		statement, err := s.C.PrepareContext(s.P.Ctx, "SELECT v FROM own_provider_view")
		if err != nil {
			s.T.Fatal(err)
		}
		defer statement.Close()
		s.Exec("GRANT own_target TO own_source WITH INHERIT TRUE, SET FALSE")
		s.Actor("own_source")
		ownershipFailure(s, "REASSIGN OWNED BY own_source TO own_target", fixture.Diagnostic{Code: "42501", Message: `must be able to SET ROLE "own_target"`})
		s.Actor("")
		ownershipProviderOwners(s, "own_source", s.Bootstrap)
		ownershipProviderStable(s, initial)
		s.Exec("GRANT own_target TO own_source WITH SET TRUE")
		s.Actor("own_source")
		s.Exec("REASSIGN OWNED BY own_source TO own_target")
		s.Actor("")
		// Extension targetNamespace is dependency metadata, not generic namespace
		// CREATE admission: target still lacks CREATE while ext alone transfers.
		if s.Backend.PG {
			ownershipBool(s, "SELECT has_schema_privilege('own_target','own_provider','CREATE')", false)
		}
		ownershipProviderOwners(s, "own_target", s.Bootstrap)
		ownershipProviderStable(s, initial)
		ownershipOwner(s, "own_provider_consumer", "own_other")
		ownershipOwner(s, "own_provider_view", "own_other")
		ownershipHotel(s, "SELECT v FROM own_provider_view")
		// Independently owned members are set using genuine PG ALTER or the existing
		// native public named-owner Restore codec; neither adds direct member grammar.
		ownershipProviderMemberOwners(s, "own_source")
		members := ownershipProviderIDs(s)
		ownershipProviderOwners(s, "own_target", "own_source")
		s.Actor("own_source")
		ownershipFailure(s, "REASSIGN OWNED BY own_source TO own_target", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema own_provider"})
		s.Actor("")
		ownershipProviderOwners(s, "own_target", "own_source")
		ownershipProviderStable(s, members)
		s.Exec("GRANT own_other TO own_target WITH INHERIT TRUE, SET FALSE")
		s.Actor("own_source")
		s.Exec("REASSIGN OWNED BY own_source TO own_target")
		s.Actor("")
		ownershipProviderOwners(s, "own_target", "own_target")
		ownershipProviderStable(s, members)
		ownershipHotel(s, "SELECT v FROM own_provider_view")
		s.Run(fixture.Exec("INSERT INTO own_provider_consumer(id) VALUES(9)", 1))
		ownershipSelect(s, "SELECT id,v FROM own_provider_consumer ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "Hotel"}, {int64(9), "Hotel"}})
		if s.Backend.PG {
			ownershipSelect(s, "SELECT policyname FROM pg_policies WHERE tablename='own_provider_consumer'", []string{"policyname"}, []string{"NAME"}, [][]any{{"own_provider_policy"}})
		}
		s.Exec("ALTER TABLE own_provider_consumer ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY")
		s.Actor("own_reader")
		ownershipPolicyRows(s, "SELECT id FROM own_provider_consumer ORDER BY id", []string{"id"}, [][]any{{int64(7)}, {int64(9)}})
		s.Actor("own_other")
		ownershipSelect(s, "SELECT id FROM own_provider_consumer", []string{"id"}, []string{"INT4"}, nil)
		s.Exec("ALTER TABLE own_provider_consumer DISABLE ROW LEVEL SECURITY")
		s.Actor("")
		rows, err := statement.QueryContext(s.P.Ctx)
		if err != nil {
			s.T.Fatal("held execution rebind", err)
		}
		n := 0
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil || v != "Hotel" {
				rows.Close()
				s.T.Fatal(v, err)
			}
			n++
		}
		err = rows.Err()
		rows.Close()
		if err != nil || n != 2 {
			s.T.Fatal(n, err)
		}
		ownershipProviderHeldHotel(s, held)
		ownershipRoleDependency(s, "own_source", "privileges for database "+ownershipDatabaseDescription(s, database))
		// REASSIGN has left received DB privileges intact; selected cleanup clears it.
		s.Exec("DROP OWNED BY own_source")
		s.Exec("DROP ROLE own_source")
		ownershipHotel(s, "SELECT own_provider.unaccent('Hôtel') AS v")
	})
}
