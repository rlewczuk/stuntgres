package reference_test

import (
	"github.com/lib/pq"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleOwnershipFamilies(t *testing.T) {
	ownershipCases(t, func(s *membershipScenario) {
		s.exec("GRANT " + s.bootstrap + " TO own_target WITH INHERIT FALSE, SET TRUE")
		ownershipInherited(s, "own_target", s.bootstrap, "USAGE", false)
		families := []struct{ name, create, alter, rename, relation string }{
			{"table", "CREATE TABLE own_table(id INT)", "ALTER TABLE own_table", "TABLE", "own_table"},
			{"partition", "CREATE TABLE own_partition(id INT) PARTITION BY RANGE(id)", "ALTER TABLE own_partition", "TABLE", "own_partition"},
			{"leaf", "CREATE TABLE own_leaf PARTITION OF own_partition FOR VALUES FROM(0) TO(10)", "ALTER TABLE own_leaf", "TABLE", "own_leaf"},
			{"view", "CREATE VIEW own_view AS SELECT 7::INT AS id", "ALTER VIEW own_view", "VIEW", "own_view"},
			{"mv", "CREATE MATERIALIZED VIEW own_mv AS SELECT 7::INT AS id", "ALTER MATERIALIZED VIEW own_mv", "MATERIALIZED VIEW", "own_mv"},
			{"sequence", "CREATE SEQUENCE own_sequence", "ALTER SEQUENCE own_sequence", "SEQUENCE", "own_sequence"},
			{"enum", "CREATE TYPE own_enum AS ENUM('a','b')", "ALTER TYPE own_enum", "TYPE", ""},
			{"domain", "CREATE DOMAIN own_domain AS INT", "ALTER DOMAIN own_domain", "DOMAIN", ""},
			{"composite", "CREATE TYPE own_composite AS(id INT)", "ALTER TYPE own_composite", "TYPE", "own_composite"},
			{"collation", "CREATE COLLATION own_collation FROM \"C\"", "ALTER COLLATION own_collation", "COLLATION", ""},
		}
		for _, f := range families {
			s.t.Run(f.name, func(t *testing.T) {
				child := *s
				child.t = t
				s := &child
				defer s.actor("")
				ownershipDefinition(s, f.create)
				s.exec(f.alter + " OWNER TO own_source")
				ownershipFailure(s, f.alter+" OWNER TO own_absent", fixture.Diagnostic{Code: "42704", Message: `role "own_absent" does not exist`})
				s.exec(f.alter + " OWNER TO own_target")
				ownershipUtilitySignature(s, f.alter+" OWNER TO own_target")
				if f.relation != "" {
					ownershipOwner(s, f.relation, "own_target")
				}
				s.actor("own_target")
				// Use actual changed-owner authority where adjacent rename
				// grammar is absent; no inherited bootstrap authority is granted.
				switch f.name {
				case "partition", "leaf", "mv", "enum", "domain", "composite":
					fixture.WithPublicSchemaOwner(s.t, s.p, s.backend, s.bootstrap, func() { s.exec(f.alter + " OWNER TO " + s.bootstrap) })
					if f.relation != "" {
						ownershipOwner(s, f.relation, s.bootstrap)
					}
					s.actor("")
					s.exec(f.alter + " OWNER TO own_target")
				default:
					fixture.WithPublicSchemaOwner(s.t, s.p, s.backend, "own_target", func() {
						s.exec(f.alter + " RENAME TO own_" + f.name + "_renamed")
						s.exec("ALTER " + f.rename + " own_" + f.name + "_renamed RENAME TO own_" + f.name)
					})
				}
				s.actor("")
			})
		}
		s.run(fixture.Exec("INSERT INTO own_table VALUES(7)", 1))
		s.run(fixture.Exec("INSERT INTO own_partition VALUES(7)", 1))
		for _, table := range []string{"own_table", "own_partition", "own_leaf", "own_view", "own_mv"} {
			ownershipRows(s, table)
		}
		ownershipSelect(s, "SELECT nextval('own_sequence') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}})
		ownershipSelect(s, "SELECT 'b'::own_enum::TEXT AS e,7::own_domain::INT AS d,'(7)'::own_composite::TEXT AS c", []string{"e", "d", "c"}, []string{"TEXT", "INT4", "TEXT"}, [][]any{{"b", int64(7), "(7)"}})
		s.exec("CREATE SCHEMA own_schema AUTHORIZATION own_source")
		s.exec("ALTER SCHEMA own_schema OWNER TO own_target")
		s.actor("own_target")
		s.exec("CREATE TABLE own_schema.proof(id INT)")
		s.actor("")
		s.exec("ALTER SCHEMA own_schema RENAME TO own_schema_renamed")
		s.exec("ALTER SCHEMA own_schema_renamed OWNER TO CURRENT_USER")
		s.exec("DROP SCHEMA own_schema_renamed CASCADE")
		// Quoted identity words stay names; unquoted selectors resolve at Execute.
		s.exec(`CREATE ROLE "current_user"`)
		defer func() {
			s.actor("")
			s.exec(`DROP OWNED BY "current_user" CASCADE`)
			s.exec(`DROP ROLE "current_user"`)
		}()
		s.exec(`ALTER TABLE own_table OWNER TO "current_user"`)
		ownershipOwner(s, "own_table", "current_user")
		for _, target := range []string{"CURRENT_ROLE", "CURRENT_USER", "SESSION_USER"} {
			s.exec("ALTER TABLE own_table OWNER TO " + target)
			ownershipOwner(s, "own_table", s.bootstrap)
		}
		s.exec(`ALTER TABLE own_table OWNER TO "own Quoted"`)
		ownershipOwner(s, "own_table", "own Quoted")
	})
}

func TestReferenceRoleOwnershipAuthority(t *testing.T) {
	ownershipCases(t, func(s *membershipScenario) {
		ownershipSeed(s, "own_auth", "own_source")
		s.exec("GRANT ALL ON own_auth TO own_actor")
		s.exec("GRANT own_target TO own_actor WITH INHERIT FALSE, SET TRUE")
		s.actor("own_actor")
		ownershipFailure(s, "ALTER TABLE own_auth OWNER TO own_target", fixture.Diagnostic{Code: "42501", Message: "must be owner of table own_auth"})
		s.actor("")
		s.exec("GRANT own_source TO own_actor WITH INHERIT FALSE, SET TRUE")
		s.actor("own_actor")
		ownershipFailure(s, "ALTER TABLE own_auth OWNER TO own_target", fixture.Diagnostic{Code: "42501", Message: "must be owner of table own_auth"})
		s.actor("")
		s.exec("GRANT own_source TO own_actor WITH INHERIT TRUE, SET FALSE")
		s.exec("GRANT own_target TO own_actor WITH INHERIT TRUE, SET FALSE")
		s.actor("own_actor")
		ownershipFailure(s, "ALTER TABLE own_auth OWNER TO own_target", fixture.Diagnostic{Code: "42501", Message: `must be able to SET ROLE "own_target"`})
		s.actor("")
		s.exec("GRANT own_target TO own_middle WITH INHERIT FALSE, SET TRUE")
		s.exec("GRANT own_middle TO own_actor WITH INHERIT FALSE, SET TRUE")
		s.exec("ALTER ROLE own_target NOLOGIN")
		s.actor("own_actor")
		fixture.WithPublicSchemaOwner(s.t, s.p, s.backend, "own_target", func() { s.exec("ALTER TABLE own_auth OWNER TO own_target") })
		s.actor("")
		ownershipOwner(s, "own_auth", "own_target")
		ownershipRows(s, "own_auth")
		// Alternate SET path works after the first path's SET option is removed.
		s.exec("ALTER TABLE own_auth OWNER TO own_source")
		s.exec("GRANT own_target TO own_alt WITH INHERIT FALSE, SET TRUE")
		s.exec("GRANT own_alt TO own_actor WITH INHERIT FALSE, SET TRUE")
		s.exec("REVOKE SET OPTION FOR own_middle FROM own_actor")
		s.actor("own_actor")
		fixture.WithPublicSchemaOwner(s.t, s.p, s.backend, "own_target", func() { s.exec("ALTER TABLE own_auth OWNER TO own_target") })
		s.actor("")
		// Target must CREATE in the containing schema, regardless of actor CREATE.
		s.exec("CREATE SCHEMA own_gate AUTHORIZATION own_source")
		s.actor("own_source")
		s.exec("CREATE SEQUENCE own_gate.s")
		s.actor("own_actor")
		ownershipFailure(s, "ALTER SEQUENCE own_gate.s OWNER TO own_target", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema own_gate"})
		s.actor("")
		// Inherited schema ownership supplies actual CREATE/USAGE, without
		// introducing the absent native schema-GRANT SQL provider.
		s.exec("GRANT own_source TO own_target WITH INHERIT TRUE, SET FALSE")
		s.actor("own_actor")
		s.exec("ALTER SEQUENCE own_gate.s OWNER TO own_target")
		s.actor("")
		// Superuser can transfer without any SET or successor CREATE path.
		s.exec("ALTER SEQUENCE own_gate.s OWNER TO own_other")
		ownershipOwner(s, "own_gate.s", "own_other")
		// Remove this lookup path before the later reverse SET membership.
		s.exec("REVOKE own_source FROM own_target")
		// Schema transfer requires the actor's database CREATE authority, plus
		// SET to the successor. Inherit the represented real database owner.
		s.exec("GRANT " + s.bootstrap + " TO own_source WITH INHERIT TRUE, SET TRUE")
		s.actor("own_source")
		s.exec("ALTER SCHEMA own_gate OWNER TO " + s.bootstrap)
		s.actor("")
		s.exec("ALTER SCHEMA own_gate OWNER TO own_source")
		s.exec("REVOKE INHERIT OPTION FOR " + s.bootstrap + " FROM own_source")
		s.exec("GRANT own_target TO own_source WITH INHERIT FALSE, SET TRUE")
		s.actor("own_source")
		database := ownershipDatabaseName(s)
		ownershipFailure(s, "ALTER SCHEMA own_gate OWNER TO own_target", fixture.Diagnostic{Code: "42501", Message: "permission denied for database " + ownershipDatabaseDescription(s, database)})
		s.actor("")
		s.exec("GRANT CREATE ON DATABASE " + pq.QuoteIdentifier(database) + " TO own_source")
		if s.backend.PG {
			ownershipBool(s, "SELECT has_database_privilege('own_target',current_database(),'CREATE')", false)
		}
		s.actor("own_source")
		s.exec("ALTER SCHEMA own_gate OWNER TO own_target")
		s.actor("")
		s.exec("REVOKE CREATE ON DATABASE " + pq.QuoteIdentifier(database) + " FROM own_source")
	})
}
