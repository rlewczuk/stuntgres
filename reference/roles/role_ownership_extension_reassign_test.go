package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoleOwnershipExtensionReassign(t *testing.T) {
	ownershipProviderCases(t, func(s *membershipScenario, database string) {
		ownershipProviderSetup(s, database, false)
		ownershipProviderOwners(s, "own_source", s.bootstrap)
		initial := ownershipProviderIDs(s)
		s.actor("own_other")
		fixture.WithPublicSchemaOwner(s.t, s.p, s.backend, "own_other", func() {
			s.exec("CREATE TABLE own_provider_consumer(id INT,v TEXT DEFAULT own_provider.unaccent('Hôtel'),CHECK(own_provider.unaccent(v)<>''))")
		})
		s.run(fixture.Exec("INSERT INTO own_provider_consumer(id) VALUES(7)", 1))
		fixture.WithPublicSchemaOwner(s.t, s.p, s.backend, "own_other", func() {
			s.exec("CREATE VIEW own_provider_view AS SELECT own_provider.unaccent(v) AS v FROM own_provider_consumer")
		})
		s.exec("CREATE POLICY own_provider_policy ON own_provider_consumer TO own_reader USING(own_provider.unaccent(v)<>'')")
		s.actor("")
		s.exec("GRANT own_other TO own_reader WITH INHERIT TRUE, SET FALSE")
		s.exec("ALTER TABLE own_provider_consumer ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY")
		s.actor("own_reader")
		ownershipPolicyRows(s, "SELECT id FROM own_provider_consumer", []string{"id"}, [][]any{{int64(7)}})
		s.actor("own_other")
		ownershipSelect(s, "SELECT id FROM own_provider_consumer", []string{"id"}, []string{"INT4"}, nil)
		s.exec("ALTER TABLE own_provider_consumer DISABLE ROW LEVEL SECURITY")
		s.actor("")
		ownershipHotel(s, "SELECT v FROM own_provider_view")
		held := ownershipProviderHeld(s, "SELECT v FROM own_provider_view")
		defer held.Close()
		statement, err := s.c.PrepareContext(s.p.Ctx, "SELECT v FROM own_provider_view")
		if err != nil {
			s.t.Fatal(err)
		}
		defer statement.Close()
		s.exec("GRANT own_target TO own_source WITH INHERIT TRUE, SET FALSE")
		s.actor("own_source")
		ownershipFailure(s, "REASSIGN OWNED BY own_source TO own_target", fixture.Diagnostic{Code: "42501", Message: `must be able to SET ROLE "own_target"`})
		s.actor("")
		ownershipProviderOwners(s, "own_source", s.bootstrap)
		ownershipProviderStable(s, initial)
		s.exec("GRANT own_target TO own_source WITH SET TRUE")
		s.actor("own_source")
		s.exec("REASSIGN OWNED BY own_source TO own_target")
		s.actor("")
		// Extension targetNamespace is dependency metadata, not generic namespace
		// CREATE admission: target still lacks CREATE while ext alone transfers.
		if s.backend.PG {
			ownershipBool(s, "SELECT has_schema_privilege('own_target','own_provider','CREATE')", false)
		}
		ownershipProviderOwners(s, "own_target", s.bootstrap)
		ownershipProviderStable(s, initial)
		ownershipOwner(s, "own_provider_consumer", "own_other")
		ownershipOwner(s, "own_provider_view", "own_other")
		ownershipHotel(s, "SELECT v FROM own_provider_view")
		// Independently owned members are set using genuine PG ALTER or the existing
		// native public named-owner Restore codec; neither adds direct member grammar.
		ownershipProviderMemberOwners(s, "own_source")
		members := ownershipProviderIDs(s)
		ownershipProviderOwners(s, "own_target", "own_source")
		s.actor("own_source")
		ownershipFailure(s, "REASSIGN OWNED BY own_source TO own_target", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema own_provider"})
		s.actor("")
		ownershipProviderOwners(s, "own_target", "own_source")
		ownershipProviderStable(s, members)
		s.exec("GRANT own_other TO own_target WITH INHERIT TRUE, SET FALSE")
		s.actor("own_source")
		s.exec("REASSIGN OWNED BY own_source TO own_target")
		s.actor("")
		ownershipProviderOwners(s, "own_target", "own_target")
		ownershipProviderStable(s, members)
		ownershipHotel(s, "SELECT v FROM own_provider_view")
		s.run(fixture.Exec("INSERT INTO own_provider_consumer(id) VALUES(9)", 1))
		ownershipSelect(s, "SELECT id,v FROM own_provider_consumer ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "Hotel"}, {int64(9), "Hotel"}})
		if s.backend.PG {
			ownershipSelect(s, "SELECT policyname FROM pg_policies WHERE tablename='own_provider_consumer'", []string{"policyname"}, []string{"NAME"}, [][]any{{"own_provider_policy"}})
		}
		s.exec("ALTER TABLE own_provider_consumer ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY")
		s.actor("own_reader")
		ownershipPolicyRows(s, "SELECT id FROM own_provider_consumer ORDER BY id", []string{"id"}, [][]any{{int64(7)}, {int64(9)}})
		s.actor("own_other")
		ownershipSelect(s, "SELECT id FROM own_provider_consumer", []string{"id"}, []string{"INT4"}, nil)
		s.exec("ALTER TABLE own_provider_consumer DISABLE ROW LEVEL SECURITY")
		s.actor("")
		rows, err := statement.QueryContext(s.p.Ctx)
		if err != nil {
			s.t.Fatal("held execution rebind", err)
		}
		n := 0
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil || v != "Hotel" {
				rows.Close()
				s.t.Fatal(v, err)
			}
			n++
		}
		err = rows.Err()
		rows.Close()
		if err != nil || n != 2 {
			s.t.Fatal(n, err)
		}
		ownershipProviderHeldHotel(s, held)
		ownershipRoleDependency(s, "own_source", "privileges for database "+ownershipDatabaseDescription(s, database))
		// REASSIGN has left received DB privileges intact; selected cleanup clears it.
		s.exec("DROP OWNED BY own_source")
		s.exec("DROP ROLE own_source")
		ownershipHotel(s, "SELECT own_provider.unaccent('Hôtel') AS v")
	})
}
