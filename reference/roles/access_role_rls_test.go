package reference_test

import (
	"github.com/lib/pq"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAccessRoleRLS(t *testing.T) {
	accessRoleCases(t, []string{"ar_actor", "ar_target", "ar_bypass", "ar_super", "ar_inherit", "ar_switch"}, func(s *membershipScenario) {
		accessRoleTable(s)
		s.exec("ALTER ROLE ar_bypass BYPASSRLS")
		s.exec("ALTER ROLE ar_super SUPERUSER")
		s.exec("GRANT ar_target TO ar_inherit WITH INHERIT TRUE, SET FALSE")
		s.exec("GRANT ar_target TO ar_switch WITH INHERIT FALSE, SET TRUE")
		s.exec("GRANT ar_bypass,ar_super TO ar_actor WITH INHERIT TRUE")
		for _, state := range []struct{ enabled, forced bool }{{false, false}, {false, true}, {true, false}, {true, true}} {
			s.actor("")
			if state.enabled {
				s.exec("ALTER TABLE ar_t ENABLE ROW LEVEL SECURITY")
			} else {
				s.exec("ALTER TABLE ar_t DISABLE ROW LEVEL SECURITY")
			}
			if state.forced {
				s.exec("ALTER TABLE ar_t FORCE ROW LEVEL SECURITY")
			} else {
				s.exec("ALTER TABLE ar_t NO FORCE ROW LEVEL SECURITY")
			}
			for _, actor := range []string{"ar_actor", "ar_target", "ar_bypass", "ar_super", "ar_inherit", "ar_switch", s.bootstrap} {
				s.actor(actor)
				active := state.enabled && actor != "ar_bypass" && actor != "ar_super" && actor != s.bootstrap && (state.forced || (actor != "ar_target" && actor != "ar_inherit"))
				accessRoleBool(s, "SELECT row_security_active('ar_t') AS v", active)
				accessRoleBool(s, "SELECT row_security_active('ar_t'::regclass::oid) AS v", active)
			}
		}
		s.actor("")
		s.exec("ALTER TABLE ar_t NO FORCE ROW LEVEL SECURITY")
		s.actor("ar_switch")
		accessRoleBool(s, "SELECT row_security_active('ar_t') AS v", true)
		s.exec("SET ROLE ar_target")
		accessRoleBool(s, "SELECT row_security_active('ar_t') AS v", false)
		s.exec("RESET ROLE")
		accessRoleBool(s, "SELECT row_security_active('ar_t') AS v", true)
	})
}

func TestReferenceAccessRoleRLSPolicies(t *testing.T) {
	accessRoleCases(t, []string{"ar_actor", "ar_target", "ar_other"}, func(s *membershipScenario) {
		accessRoleTable(s)
		s.exec("ALTER TABLE ar_t ENABLE ROW LEVEL SECURITY")
		for _, policy := range []string{
			"", "CREATE POLICY ar_policy ON ar_t TO ar_other USING(true)",
			"CREATE POLICY ar_policy ON ar_t AS RESTRICTIVE TO ar_actor USING(true)",
			"CREATE POLICY ar_policy ON ar_t TO ar_actor USING(true)",
			"CREATE POLICY ar_policy ON ar_t TO ar_actor USING(false)",
			"CREATE POLICY ar_policy ON ar_t TO ar_actor USING(id/0 > 0)",
		} {
			s.actor("")
			if policy != "" {
				s.exec(policy)
			}
			s.actor("ar_actor")
			// No SELECT ACL, no rows scanned, no policy expression evaluation.
			accessRoleBool(s, "SELECT row_security_active('ar_t') AS v", true)
			predefinedFailure(s, "SELECT id FROM ar_t", fixture.Diagnostic{Code: "42501", Message: "permission denied for table ar_t"}, true, "execute")
			if s.backend.PG {
				s.exec("SET row_security=off")
				accessRoleBool(s, "SELECT row_security_active('ar_t') AS v", true)
				s.exec("SET row_security=on")
			}
			s.actor("")
			if policy != "" {
				s.exec("DROP POLICY ar_policy ON ar_t")
			}
		}
		s.exec("GRANT SELECT ON ar_t TO ar_actor")
		s.exec("CREATE POLICY ar_policy ON ar_t TO ar_actor USING(true)")
		s.actor("ar_actor")
		accessRoleBool(s, "SELECT row_security_active('ar_t') AS v", true)
		if !s.backend.PG {
			predefinedFailure(s, "SELECT id FROM ar_t", fixture.Diagnostic{Code: "0A000", Message: "row-security policy expression enforcement is unsupported"}, true, "execute")
		}
	})
}

func TestReferenceAccessRoleRLSNames(t *testing.T) {
	accessRoleCases(t, []string{"ar_actor", "ar_target"}, func(s *membershipScenario) {
		accessRoleTable(s)
		s.exec("ALTER TABLE ar_t ENABLE ROW LEVEL SECURITY")
		s.exec(`CREATE TABLE "ar.dot table"(id INT)`)
		s.exec(`CREATE TABLE "123"(id INT)`)
		s.exec("CREATE VIEW ar_v AS SELECT id FROM ar_t")
		s.run(fixture.Exec("CREATE MATERIALIZED VIEW ar_mv AS SELECT id FROM ar_t", 0))
		s.exec("CREATE SEQUENCE ar_seq")
		s.exec("CREATE INDEX ar_idx ON ar_t(id)")
		s.exec("CREATE TABLE ar_parent(id INT) PARTITION BY RANGE(id)")
		s.exec("CREATE TABLE ar_child PARTITION OF ar_parent FOR VALUES FROM(0) TO(10)")
		s.exec("ALTER TABLE ar_parent ENABLE ROW LEVEL SECURITY")
		s.actor("ar_actor")
		for _, name := range []string{"ar_t", "public.ar_t", `"public"."ar_t"`} {
			accessRoleBool(s, "SELECT row_security_active($1::text) AS v", true, name)
		}
		for _, name := range []string{`"ar.dot table"`, "123", "ar_v", "ar_mv", "ar_seq", "ar_idx", "pg_catalog.pg_class", "ar_child"} {
			accessRoleBool(s, "SELECT row_security_active($1::text) AS v", false, name)
		}
		accessRoleBool(s, "SELECT row_security_active('ar_parent') AS v", true)
		for _, oid := range []int64{0, 1259, 4294967295} {
			accessRoleBool(s, "SELECT row_security_active($1::oid) AS v", false, oid)
		}
		var db string
		if err := s.c.QueryRowContext(s.p.Ctx, "SELECT current_database()").Scan(&db); err != nil {
			s.t.Fatal(err)
		}
		accessRoleBool(s, "SELECT row_security_active($1) AS v", true, pq.QuoteIdentifier(db)+".public.ar_t")
		for _, tc := range []struct{ name, code, message string }{
			{"ar_missing", "42P01", `relation "ar_missing" does not exist`},
			{"missing_schema.ar_t", "3F000", `schema "missing_schema" does not exist`},
			{"999999", "42P01", `relation "999999" does not exist`},
			{"", "42602", "invalid name syntax"},
			{`"unterminated`, "42602", "invalid name syntax"},
			{"a.b.c.d", "42601", `improper relation name (too many dotted names): a.b.c.d`},
			{"ar_other_db.public.ar_t", "0A000", `cross-database references are not implemented: "ar_other_db.public.ar_t"`},
		} {
			s.t.Run(tc.name, func(t *testing.T) {
				child := *s
				child.t = t
				accessRoleRuntimeError(&child, "SELECT row_security_active($1::text)", tc.code, tc.message, tc.name)
			})
		}
		s.actor("")
		s.exec("CREATE TEMP TABLE ar_t(id INT)")
		accessRoleBool(s, "SELECT row_security_active('ar_t') AS v", false)
		s.exec("DROP TABLE pg_temp.ar_t")
		s.actor("ar_actor")
		accessRoleBool(s, "SELECT row_security_active('ar_t') AS v", true)
		s.actor("")
		s.exec("CREATE SCHEMA ar_private AUTHORIZATION ar_target")
		s.exec("CREATE TABLE ar_private.hidden(id INT)")
		s.exec("ALTER TABLE ar_private.hidden ENABLE ROW LEVEL SECURITY")
		var hidden int64
		if err := s.c.QueryRowContext(s.p.Ctx, "SELECT oid FROM pg_class WHERE relname='hidden'").Scan(&hidden); err != nil {
			s.t.Fatal(err)
		}
		s.actor("ar_actor")
		accessRoleRuntimeError(s, "SELECT row_security_active('ar_private.hidden')", "42501", "permission denied for schema ar_private")
		accessRoleBool(s, "SELECT row_security_active($1::oid) AS v", true, hidden)
		s.actor("")
		for _, schema := range []string{"ar_first", "ar_second"} {
			s.exec("CREATE SCHEMA " + schema + " AUTHORIZATION ar_actor")
			s.exec("CREATE TABLE " + schema + ".ar_path_t(id INT)")
		}
		s.exec("ALTER TABLE ar_first.ar_path_t ENABLE ROW LEVEL SECURITY")
		s.actor("ar_actor")
		path := predefinedHeld(s, "SELECT row_security_active($1::text) AS v")
		s.exec("SET search_path TO ar_first,ar_second,public")
		accessRoleHeldBool(s, path, true, "ar_path_t")
		s.exec("SET search_path TO ar_second,ar_first,public")
		accessRoleHeldBool(s, path, false, "ar_path_t")
		s.exec("SET search_path TO public")
	})
}
