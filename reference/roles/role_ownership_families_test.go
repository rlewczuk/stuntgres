package reference_test

import (
	"github.com/lib/pq"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleOwnershipFamilies(t *testing.T) {
	ownershipCases(t, func(s *fixture.RoleMembershipScenario) {
		s.Exec("GRANT " + s.Bootstrap + " TO own_target WITH INHERIT FALSE, SET TRUE")
		ownershipInherited(s, "own_target", s.Bootstrap, "USAGE", false)
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
			s.T.Run(f.name, func(t *testing.T) {
				child := *s
				child.T = t
				s := &child
				defer s.Actor("")
				ownershipDefinition(s, f.create)
				s.Exec(f.alter + " OWNER TO own_source")
				ownershipFailure(s, f.alter+" OWNER TO own_absent", fixture.Diagnostic{Code: "42704", Message: `role "own_absent" does not exist`})
				s.Exec(f.alter + " OWNER TO own_target")
				ownershipUtilitySignature(s, f.alter+" OWNER TO own_target")
				if f.relation != "" {
					ownershipOwner(s, f.relation, "own_target")
				}
				s.Actor("own_target")
				// Use actual changed-owner authority where adjacent rename
				// grammar is absent; no inherited bootstrap authority is granted.
				switch f.name {
				case "partition", "leaf", "mv", "enum", "domain", "composite":
					fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, s.Bootstrap, func() { s.Exec(f.alter + " OWNER TO " + s.Bootstrap) })
					if f.relation != "" {
						ownershipOwner(s, f.relation, s.Bootstrap)
					}
					s.Actor("")
					s.Exec(f.alter + " OWNER TO own_target")
				default:
					fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "own_target", func() {
						s.Exec(f.alter + " RENAME TO own_" + f.name + "_renamed")
						s.Exec("ALTER " + f.rename + " own_" + f.name + "_renamed RENAME TO own_" + f.name)
					})
				}
				s.Actor("")
			})
		}
		s.Run(fixture.Exec("INSERT INTO own_table VALUES(7)", 1))
		s.Run(fixture.Exec("INSERT INTO own_partition VALUES(7)", 1))
		for _, table := range []string{"own_table", "own_partition", "own_leaf", "own_view", "own_mv"} {
			ownershipRows(s, table)
		}
		ownershipSelect(s, "SELECT nextval('own_sequence') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}})
		ownershipSelect(s, "SELECT 'b'::own_enum::TEXT AS e,7::own_domain::INT AS d,'(7)'::own_composite::TEXT AS c", []string{"e", "d", "c"}, []string{"TEXT", "INT4", "TEXT"}, [][]any{{"b", int64(7), "(7)"}})
		s.Exec("CREATE SCHEMA own_schema AUTHORIZATION own_source")
		s.Exec("ALTER SCHEMA own_schema OWNER TO own_target")
		s.Actor("own_target")
		s.Exec("CREATE TABLE own_schema.proof(id INT)")
		s.Actor("")
		s.Exec("ALTER SCHEMA own_schema RENAME TO own_schema_renamed")
		s.Exec("ALTER SCHEMA own_schema_renamed OWNER TO CURRENT_USER")
		s.Exec("DROP SCHEMA own_schema_renamed CASCADE")
		// Quoted identity words stay names; unquoted selectors resolve at Execute.
		s.Exec(`CREATE ROLE "current_user"`)
		defer func() {
			s.Actor("")
			s.Exec(`DROP OWNED BY "current_user" CASCADE`)
			s.Exec(`DROP ROLE "current_user"`)
		}()
		s.Exec(`ALTER TABLE own_table OWNER TO "current_user"`)
		ownershipOwner(s, "own_table", "current_user")
		for _, target := range []string{"CURRENT_ROLE", "CURRENT_USER", "SESSION_USER"} {
			s.Exec("ALTER TABLE own_table OWNER TO " + target)
			ownershipOwner(s, "own_table", s.Bootstrap)
		}
		s.Exec(`ALTER TABLE own_table OWNER TO "own Quoted"`)
		ownershipOwner(s, "own_table", "own Quoted")
	})
}

func TestReferenceRoleOwnershipAuthority(t *testing.T) {
	ownershipCases(t, func(s *fixture.RoleMembershipScenario) {
		ownershipSeed(s, "own_auth", "own_source")
		s.Exec("GRANT ALL ON own_auth TO own_actor")
		s.Exec("GRANT own_target TO own_actor WITH INHERIT FALSE, SET TRUE")
		s.Actor("own_actor")
		ownershipFailure(s, "ALTER TABLE own_auth OWNER TO own_target", fixture.Diagnostic{Code: "42501", Message: "must be owner of table own_auth"})
		s.Actor("")
		s.Exec("GRANT own_source TO own_actor WITH INHERIT FALSE, SET TRUE")
		s.Actor("own_actor")
		ownershipFailure(s, "ALTER TABLE own_auth OWNER TO own_target", fixture.Diagnostic{Code: "42501", Message: "must be owner of table own_auth"})
		s.Actor("")
		s.Exec("GRANT own_source TO own_actor WITH INHERIT TRUE, SET FALSE")
		s.Exec("GRANT own_target TO own_actor WITH INHERIT TRUE, SET FALSE")
		s.Actor("own_actor")
		ownershipFailure(s, "ALTER TABLE own_auth OWNER TO own_target", fixture.Diagnostic{Code: "42501", Message: `must be able to SET ROLE "own_target"`})
		s.Actor("")
		s.Exec("GRANT own_target TO own_middle WITH INHERIT FALSE, SET TRUE")
		s.Exec("GRANT own_middle TO own_actor WITH INHERIT FALSE, SET TRUE")
		s.Exec("ALTER ROLE own_target NOLOGIN")
		s.Actor("own_actor")
		fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "own_target", func() { s.Exec("ALTER TABLE own_auth OWNER TO own_target") })
		s.Actor("")
		ownershipOwner(s, "own_auth", "own_target")
		ownershipRows(s, "own_auth")
		// Alternate SET path works after the first path's SET option is removed.
		s.Exec("ALTER TABLE own_auth OWNER TO own_source")
		s.Exec("GRANT own_target TO own_alt WITH INHERIT FALSE, SET TRUE")
		s.Exec("GRANT own_alt TO own_actor WITH INHERIT FALSE, SET TRUE")
		s.Exec("REVOKE SET OPTION FOR own_middle FROM own_actor")
		s.Actor("own_actor")
		fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "own_target", func() { s.Exec("ALTER TABLE own_auth OWNER TO own_target") })
		s.Actor("")
		// Target must CREATE in the containing schema, regardless of actor CREATE.
		s.Exec("CREATE SCHEMA own_gate AUTHORIZATION own_source")
		s.Actor("own_source")
		s.Exec("CREATE SEQUENCE own_gate.s")
		s.Actor("own_actor")
		ownershipFailure(s, "ALTER SEQUENCE own_gate.s OWNER TO own_target", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema own_gate"})
		s.Actor("")
		// Inherited schema ownership supplies actual CREATE/USAGE, without
		// introducing the absent native schema-GRANT SQL provider.
		s.Exec("GRANT own_source TO own_target WITH INHERIT TRUE, SET FALSE")
		s.Actor("own_actor")
		s.Exec("ALTER SEQUENCE own_gate.s OWNER TO own_target")
		s.Actor("")
		// Superuser can transfer without any SET or successor CREATE path.
		s.Exec("ALTER SEQUENCE own_gate.s OWNER TO own_other")
		ownershipOwner(s, "own_gate.s", "own_other")
		// Remove this lookup path before the later reverse SET membership.
		s.Exec("REVOKE own_source FROM own_target")
		// Schema transfer requires the actor's database CREATE authority, plus
		// SET to the successor. Inherit the represented real database owner.
		s.Exec("GRANT " + s.Bootstrap + " TO own_source WITH INHERIT TRUE, SET TRUE")
		s.Actor("own_source")
		s.Exec("ALTER SCHEMA own_gate OWNER TO " + s.Bootstrap)
		s.Actor("")
		s.Exec("ALTER SCHEMA own_gate OWNER TO own_source")
		s.Exec("REVOKE INHERIT OPTION FOR " + s.Bootstrap + " FROM own_source")
		s.Exec("GRANT own_target TO own_source WITH INHERIT FALSE, SET TRUE")
		s.Actor("own_source")
		database := ownershipDatabaseName(s)
		ownershipFailure(s, "ALTER SCHEMA own_gate OWNER TO own_target", fixture.Diagnostic{Code: "42501", Message: "permission denied for database " + ownershipDatabaseDescription(s, database)})
		s.Actor("")
		s.Exec("GRANT CREATE ON DATABASE " + pq.QuoteIdentifier(database) + " TO own_source")
		if s.Backend.PG {
			ownershipBool(s, "SELECT has_database_privilege('own_target',current_database(),'CREATE')", false)
		}
		s.Actor("own_source")
		s.Exec("ALTER SCHEMA own_gate OWNER TO own_target")
		s.Actor("")
		s.Exec("REVOKE CREATE ON DATABASE " + pq.QuoteIdentifier(database) + " FROM own_source")
	})
}
