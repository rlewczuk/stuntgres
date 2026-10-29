package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleOwnershipRestrictAtomicity(t *testing.T) {
	ownershipCases(t, func(s *fixture.RoleMembershipScenario) {
		ownershipSeed(s, "own_restrict", "own_source")
		ownershipSeed(s, "own_granted", "own_other")
		s.Exec("GRANT SELECT ON own_granted TO own_source WITH GRANT OPTION")
		s.Actor("own_source")
		s.Exec("GRANT SELECT ON own_granted TO own_downstream")
		s.Actor("")
		s.Exec("CREATE POLICY own_policy_keep ON own_granted TO own_source,own_reader USING(id>0)")
		s.Exec("GRANT own_middle TO own_source WITH ADMIN TRUE")
		s.Exec("GRANT own_middle TO own_downstream GRANTED BY own_source")
		s.Exec("CREATE VIEW own_external_view AS SELECT id FROM own_restrict")
		s.Exec("ALTER TABLE own_granted ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY")
		s.Actor("own_source")
		ownershipPolicyRows(s, "SELECT id FROM own_granted", []string{"id"}, [][]any{{int64(7)}})
		s.Actor("")
		s.ClearNotices()
		ownershipFailure(s, "DROP OWNED BY own_source RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop desired object(s) because other objects depend on them", Detail: "view own_external_view depends on table own_restrict", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
		ownershipNotices(s)
		ownershipOwner(s, "own_restrict", "own_source")
		ownershipRows(s, "own_restrict")
		ownershipRows(s, "own_external_view")
		s.Actor("own_source")
		ownershipPolicyRows(s, "SELECT id FROM own_granted", []string{"id"}, [][]any{{int64(7)}})
		s.Actor("")
		s.Exec("ALTER TABLE own_granted DISABLE ROW LEVEL SECURITY")
		s.Actor("own_downstream")
		ownershipRows(s, "own_granted")
		s.Actor("")
		ownershipInherited(s, "own_downstream", "own_middle", "MEMBER", true)
		ownershipSelect(s, "SELECT roles::TEXT AS roles FROM pg_policies WHERE policyname='own_policy_keep'", []string{"roles"}, []string{"TEXT"}, [][]any{{"{own_reader,own_source}"}})
		s.Exec("ALTER TABLE own_granted ENABLE ROW LEVEL SECURITY")
		s.Exec("GRANT SELECT ON own_granted TO own_reader")
		s.Actor("own_reader")
		ownershipPolicyRows(s, "SELECT id FROM own_granted", []string{"id"}, [][]any{{int64(7)}})
		s.Actor("own_downstream")
		ownershipSelect(s, "SELECT id FROM own_granted", []string{"id"}, []string{"INT4"}, nil)
		s.Actor("")
		s.Exec("ALTER TABLE own_granted DISABLE ROW LEVEL SECURITY")
		for _, q := range []string{"DROP OWNED BY own_source,own_absent CASCADE", "REASSIGN OWNED BY own_source,own_absent TO own_target"} {
			ownershipFailure(s, q, fixture.Diagnostic{Code: "42704", Message: `role "own_absent" does not exist`})
			ownershipOwner(s, "own_restrict", "own_source")
			ownershipRows(s, "own_granted")
		}
		s.Exec("GRANT own_source TO own_actor WITH INHERIT TRUE, SET FALSE")
		s.Actor("own_actor")
		ownershipFailure(s, "DROP OWNED BY own_source,own_other CASCADE", fixture.Diagnostic{Code: "42501", Message: "permission denied to drop objects", Detail: `Only roles with privileges of role "own_other" may drop objects owned by it.`})
		s.Actor("")
		ownershipRows(s, "own_restrict")
		if s.Backend.PG {
			ownershipSeed(s, "own_postgres_drop", "postgres")
			s.Exec("DROP OWNED BY postgres CASCADE")
			ownershipCount(s, "SELECT count(*) FROM pg_class WHERE relname='own_postgres_drop'", 0)
			ownershipRows(s, "own_restrict")
			ownershipRows(s, "own_external_view")
		}
		ownershipSelect(s, "SELECT oid::BIGINT AS oid FROM pg_roles WHERE rolname='pg_database_owner'", []string{"oid"}, []string{"INT8"}, [][]any{{int64(6171)}})
		ownershipFailure(s, "DROP OWNED BY own_source,pg_database_owner CASCADE", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop objects owned by role pg_database_owner because they are required by the database system"})
		ownershipRows(s, "own_restrict")
		ownershipRows(s, "own_external_view")
		s.Actor("own_source")
		ownershipRows(s, "own_granted")
		s.Actor("")
		ownershipInherited(s, "own_downstream", "own_middle", "MEMBER", true)
		s.Exec("DROP VIEW own_external_view")
		// All roots of one source and all sources are preflighted as one union.
		s.Exec("GRANT SELECT ON own_granted TO own_target")
		s.Actor("own_source")
		fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "own_source", func() { s.Exec("CREATE VIEW own_union_same AS SELECT id FROM own_restrict") })
		s.Actor("own_target")
		fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "own_target", func() { s.Exec("CREATE VIEW own_union_other AS SELECT id FROM own_granted") })
		s.Actor("")
		s.Exec("DROP OWNED BY own_source RESTRICT")
		ownershipCount(s, "SELECT count(*) FROM pg_class WHERE relname='own_restrict' OR relname='own_union_same'", 0)
		s.Exec("DROP OWNED BY own_other,own_target RESTRICT")
		ownershipCount(s, "SELECT count(*) FROM pg_class WHERE relname='own_granted' OR relname='own_union_other'", 0)
		ownershipBool(s, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='own_source')", true)
		s.Actor("own_source")
		fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "own_source", func() { s.Exec("CREATE TABLE own_after_drop(id INT)") })
		s.Actor("")
	})
}

func TestReferenceRoleOwnershipCascade(t *testing.T) {
	ownershipCases(t, func(s *fixture.RoleMembershipScenario) {
		s.Actor("own_source")
		fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "own_source", func() {
			for _, q := range []string{
				"CREATE TABLE own_cascade(id SERIAL PRIMARY KEY)",
				"CREATE DOMAIN own_cascade_domain AS INT",
				"CREATE SEQUENCE own_cascade_sequence",
			} {
				s.Exec(q)
			}
		})
		s.Run(fixture.Exec("INSERT INTO own_cascade VALUES(7)", 1))
		s.Exec("GRANT SELECT,REFERENCES ON own_cascade TO own_other")
		s.Actor("")
		s.Exec("GRANT own_source TO own_other WITH INHERIT TRUE, SET FALSE")
		s.Flags("own_source", "own_other", s.Bootstrap, 1, false, true, false)
		s.Actor("own_other")
		fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "own_other", func() {
			for _, q := range []string{
				"CREATE VIEW own_cascade_v AS SELECT id FROM own_cascade",
				"CREATE VIEW own_cascade_v2 AS SELECT id FROM own_cascade_v",
				"CREATE TABLE own_fk_survivor(id INT REFERENCES own_cascade(id),keep INT)",
				"CREATE TABLE own_type_survivor(dropme own_cascade_domain,keep INT,calc INT DEFAULT 7::own_cascade_domain,CONSTRAINT own_cast_check CHECK(keep::own_cascade_domain>0))",
				"CREATE POLICY own_cast_policy ON own_type_survivor USING(keep::own_cascade_domain>0)",
				"CREATE TABLE own_default_survivor(id INT DEFAULT nextval('own_cascade_sequence'),keep INT)",
				"CREATE SEQUENCE own_cascade_sequence_nearby",
			} {
				s.Exec(q)
			}
		})
		s.Run(fixture.Exec("INSERT INTO own_fk_survivor VALUES(7,9)", 1))
		s.Run(fixture.Exec("INSERT INTO own_type_survivor VALUES(7,9,7)", 1))
		s.Run(fixture.Exec("INSERT INTO own_default_survivor(keep) VALUES(9)", 1))
		s.Exec("ALTER TABLE own_type_survivor ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY")
		ownershipPolicyRows(s, "SELECT keep FROM own_type_survivor", []string{"keep"}, [][]any{{int64(9)}})
		s.Actor("")
		s.ClearNotices()
		ownershipFailureDetailBag(s, "DROP OWNED BY own_source", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop desired object(s) because other objects depend on them", Detail: "view own_cascade_v depends on table own_cascade\nview own_cascade_v2 depends on view own_cascade_v\nconstraint own_fk_survivor_id_fkey on table own_fk_survivor depends on table own_cascade\ncolumn dropme of table own_type_survivor depends on type own_cascade_domain\ndefault value for column calc of table own_type_survivor depends on type own_cascade_domain\nconstraint own_cast_check on table own_type_survivor depends on type own_cascade_domain\npolicy own_cast_policy on table own_type_survivor depends on type own_cascade_domain\ndefault value for column id of table own_default_survivor depends on sequence own_cascade_sequence", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
		ownershipNotices(s)
		ownershipSelect(s, "SELECT dropme::INT AS dropme,keep,calc FROM own_type_survivor", []string{"dropme", "keep", "calc"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(7), int64(9), int64(7)}})
		s.Exec("DROP OWNED BY own_source CASCADE")
		ownershipNoticesDetailBag(s, ownershipCascadeNotice("drop cascades to 8 other objects", "drop cascades to view own_cascade_v\ndrop cascades to view own_cascade_v2\ndrop cascades to constraint own_fk_survivor_id_fkey on table own_fk_survivor\ndrop cascades to column dropme of table own_type_survivor\ndrop cascades to default value for column calc of table own_type_survivor\ndrop cascades to constraint own_cast_check on table own_type_survivor\ndrop cascades to policy own_cast_policy on table own_type_survivor\ndrop cascades to default value for column id of table own_default_survivor"))
		ownershipCount(s, "SELECT count(*) FROM pg_class WHERE relname='own_cascade' OR relname='own_cascade_pkey' OR relname='own_cascade_id_seq' OR relname='own_cascade_v' OR relname='own_cascade_v2' OR relname='own_cascade_sequence'", 0)
		ownershipSelect(s, "SELECT id,keep FROM own_fk_survivor", []string{"id", "keep"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(9)}})
		ownershipCount(s, "SELECT count(*) FROM pg_constraint WHERE conrelid='own_fk_survivor'::regclass::oid AND contype='f'", 0)
		ownershipSelect(s, "SELECT keep,calc FROM own_type_survivor", []string{"keep", "calc"}, []string{"INT4", "INT4"}, [][]any{{int64(9), int64(7)}})
		s.Actor("own_other")
		ownershipSelect(s, "SELECT keep FROM own_type_survivor", []string{"keep"}, []string{"INT4"}, nil)
		ownershipFailure(s, "INSERT INTO own_type_survivor(keep) VALUES(0)", fixture.Diagnostic{Code: "42501", Message: `new row violates row-level security policy for table "own_type_survivor"`})
		s.Exec("ALTER TABLE own_type_survivor DISABLE ROW LEVEL SECURITY")
		s.Run(fixture.Exec("INSERT INTO own_type_survivor(keep) VALUES(0)", 1))
		s.Actor("")
		ownershipSelect(s, "SELECT keep,calc FROM own_type_survivor ORDER BY keep", []string{"keep", "calc"}, []string{"INT4", "INT4"}, [][]any{{int64(0), nil}, {int64(9), int64(7)}})
		ownershipCount(s, "SELECT count(*) FROM pg_policies WHERE policyname='own_cast_policy'", 0)
		ownershipFailure(s, "ALTER POLICY own_cast_policy ON own_type_survivor", fixture.Diagnostic{Code: "42704", Message: `policy "own_cast_policy" for table "own_type_survivor" does not exist`})
		s.Run(fixture.Exec("INSERT INTO own_default_survivor(keep) VALUES(9)", 1))
		ownershipSelect(s, "SELECT id,keep FROM own_default_survivor ORDER BY id NULLS LAST", []string{"id", "keep"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(9)}, {nil, int64(9)}})
		ownershipOwner(s, "own_cascade_sequence_nearby", "own_other")
		ownershipCount(s, "SELECT count(*) FROM pg_type WHERE typname IN('own_cascade','_own_cascade','own_cascade_domain','_own_cascade_domain')", 0)
		s.Exec("DROP ROLE own_source")
	})
}
