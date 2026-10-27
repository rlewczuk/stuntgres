package reference_test

import (
	"github.com/lib/pq"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAccessRoleRLS(t *testing.T) {
	fixture.AccessRoleCases(t, []string{"ar_actor", "ar_target", "ar_bypass", "ar_super", "ar_inherit", "ar_switch"}, func(s *fixture.RoleMembershipScenario) {
		fixture.AccessRoleTable(s)
		s.Exec("ALTER ROLE ar_bypass BYPASSRLS")
		s.Exec("ALTER ROLE ar_super SUPERUSER")
		s.Exec("GRANT ar_target TO ar_inherit WITH INHERIT TRUE, SET FALSE")
		s.Exec("GRANT ar_target TO ar_switch WITH INHERIT FALSE, SET TRUE")
		s.Exec("GRANT ar_bypass,ar_super TO ar_actor WITH INHERIT TRUE")
		for _, state := range []struct{ enabled, forced bool }{{false, false}, {false, true}, {true, false}, {true, true}} {
			s.Actor("")
			if state.enabled {
				s.Exec("ALTER TABLE ar_t ENABLE ROW LEVEL SECURITY")
			} else {
				s.Exec("ALTER TABLE ar_t DISABLE ROW LEVEL SECURITY")
			}
			if state.forced {
				s.Exec("ALTER TABLE ar_t FORCE ROW LEVEL SECURITY")
			} else {
				s.Exec("ALTER TABLE ar_t NO FORCE ROW LEVEL SECURITY")
			}
			for _, actor := range []string{"ar_actor", "ar_target", "ar_bypass", "ar_super", "ar_inherit", "ar_switch", s.Bootstrap} {
				s.Actor(actor)
				active := state.enabled && actor != "ar_bypass" && actor != "ar_super" && actor != s.Bootstrap && (state.forced || (actor != "ar_target" && actor != "ar_inherit"))
				fixture.AccessRoleBool(s, "SELECT row_security_active('ar_t') AS v", active)
				fixture.AccessRoleBool(s, "SELECT row_security_active('ar_t'::regclass::oid) AS v", active)
			}
		}
		s.Actor("")
		s.Exec("ALTER TABLE ar_t NO FORCE ROW LEVEL SECURITY")
		s.Actor("ar_switch")
		fixture.AccessRoleBool(s, "SELECT row_security_active('ar_t') AS v", true)
		s.Exec("SET ROLE ar_target")
		fixture.AccessRoleBool(s, "SELECT row_security_active('ar_t') AS v", false)
		s.Exec("RESET ROLE")
		fixture.AccessRoleBool(s, "SELECT row_security_active('ar_t') AS v", true)
	})
}

func TestReferenceAccessRoleRLSPolicies(t *testing.T) {
	fixture.AccessRoleCases(t, []string{"ar_actor", "ar_target", "ar_other"}, func(s *fixture.RoleMembershipScenario) {
		fixture.AccessRoleTable(s)
		s.Exec("ALTER TABLE ar_t ENABLE ROW LEVEL SECURITY")
		for _, policy := range []string{
			"", "CREATE POLICY ar_policy ON ar_t TO ar_other USING(true)",
			"CREATE POLICY ar_policy ON ar_t AS RESTRICTIVE TO ar_actor USING(true)",
			"CREATE POLICY ar_policy ON ar_t TO ar_actor USING(true)",
			"CREATE POLICY ar_policy ON ar_t TO ar_actor USING(false)",
			"CREATE POLICY ar_policy ON ar_t TO ar_actor USING(id/0 > 0)",
		} {
			s.Actor("")
			if policy != "" {
				s.Exec(policy)
			}
			s.Actor("ar_actor")
			// No SELECT ACL, no rows scanned, no policy expression evaluation.
			fixture.AccessRoleBool(s, "SELECT row_security_active('ar_t') AS v", true)
			predefinedFailure(s, "SELECT id FROM ar_t", fixture.Diagnostic{Code: "42501", Message: "permission denied for table ar_t"}, true, "execute")
			s.Exec("SET row_security=off")
			fixture.AccessRoleBool(s, "SELECT row_security_active('ar_t') AS v", true)
			s.Exec("SET row_security=on")
			s.Actor("")
			if policy != "" {
				s.Exec("DROP POLICY ar_policy ON ar_t")
			}
		}
		s.Exec("GRANT SELECT ON ar_t TO ar_actor")
		s.Exec("CREATE POLICY ar_policy ON ar_t TO ar_actor USING(true)")
		s.Actor("ar_actor")
		fixture.AccessRoleBool(s, "SELECT row_security_active('ar_t') AS v", true)
		predefinedSelect(s, "SELECT id FROM ar_t", []string{"id"}, []string{"INT4"}, nil)
	})
}

func TestReferenceAccessRoleRLSNames(t *testing.T) {
	fixture.AccessRoleCases(t, []string{"ar_actor", "ar_target"}, func(s *fixture.RoleMembershipScenario) {
		fixture.AccessRoleTable(s)
		s.Exec("ALTER TABLE ar_t ENABLE ROW LEVEL SECURITY")
		s.Exec(`CREATE TABLE "ar.dot table"(id INT)`)
		s.Exec(`CREATE TABLE "123"(id INT)`)
		s.Exec("CREATE VIEW ar_v AS SELECT id FROM ar_t")
		s.Run(fixture.Exec("CREATE MATERIALIZED VIEW ar_mv AS SELECT id FROM ar_t", 0))
		s.Exec("CREATE SEQUENCE ar_seq")
		s.Exec("CREATE INDEX ar_idx ON ar_t(id)")
		s.Exec("CREATE TABLE ar_parent(id INT) PARTITION BY RANGE(id)")
		s.Exec("CREATE TABLE ar_child PARTITION OF ar_parent FOR VALUES FROM(0) TO(10)")
		s.Exec("ALTER TABLE ar_parent ENABLE ROW LEVEL SECURITY")
		s.Actor("ar_actor")
		for _, name := range []string{"ar_t", "public.ar_t", `"public"."ar_t"`} {
			fixture.AccessRoleBool(s, "SELECT row_security_active($1::text) AS v", true, name)
		}
		for _, name := range []string{`"ar.dot table"`, "123", "ar_v", "ar_mv", "ar_seq", "ar_idx", "pg_catalog.pg_class", "ar_child"} {
			fixture.AccessRoleBool(s, "SELECT row_security_active($1::text) AS v", false, name)
		}
		fixture.AccessRoleBool(s, "SELECT row_security_active('ar_parent') AS v", true)
		for _, oid := range []int64{0, 1259, 4294967295} {
			fixture.AccessRoleBool(s, "SELECT row_security_active($1::oid) AS v", false, oid)
		}
		var db string
		if err := s.C.QueryRowContext(s.P.Ctx, "SELECT current_database()").Scan(&db); err != nil {
			s.T.Fatal(err)
		}
		fixture.AccessRoleBool(s, "SELECT row_security_active($1) AS v", true, pq.QuoteIdentifier(db)+".public.ar_t")
		for _, tc := range []struct{ name, code, message string }{
			{"ar_missing", "42P01", `relation "ar_missing" does not exist`},
			{"missing_schema.ar_t", "3F000", `schema "missing_schema" does not exist`},
			{"999999", "42P01", `relation "999999" does not exist`},
			{"", "42602", "invalid name syntax"},
			{`"unterminated`, "42602", "invalid name syntax"},
			{"a.b.c.d", "42601", `improper relation name (too many dotted names): a.b.c.d`},
			{"ar_other_db.public.ar_t", "0A000", `cross-database references are not implemented: "ar_other_db.public.ar_t"`},
		} {
			s.T.Run(tc.name, func(t *testing.T) {
				child := *s
				child.T = t
				fixture.AccessRoleRuntimeError(&child, "SELECT row_security_active($1::text)", tc.code, tc.message, tc.name)
			})
		}
		s.Actor("")
		s.Exec("CREATE TEMP TABLE ar_t(id INT)")
		fixture.AccessRoleBool(s, "SELECT row_security_active('ar_t') AS v", false)
		s.Exec("DROP TABLE pg_temp.ar_t")
		s.Actor("ar_actor")
		fixture.AccessRoleBool(s, "SELECT row_security_active('ar_t') AS v", true)
		s.Actor("")
		s.Exec("CREATE SCHEMA ar_private AUTHORIZATION ar_target")
		s.Exec("CREATE TABLE ar_private.hidden(id INT)")
		s.Exec("ALTER TABLE ar_private.hidden ENABLE ROW LEVEL SECURITY")
		var hidden int64
		if err := s.C.QueryRowContext(s.P.Ctx, "SELECT oid FROM pg_class WHERE relname='hidden'").Scan(&hidden); err != nil {
			s.T.Fatal(err)
		}
		s.Actor("ar_actor")
		fixture.AccessRoleRuntimeError(s, "SELECT row_security_active('ar_private.hidden')", "42501", "permission denied for schema ar_private")
		fixture.AccessRoleBool(s, "SELECT row_security_active($1::oid) AS v", true, hidden)
		s.Actor("")
		for _, schema := range []string{"ar_first", "ar_second"} {
			s.Exec("CREATE SCHEMA " + schema + " AUTHORIZATION ar_actor")
			s.Exec("CREATE TABLE " + schema + ".ar_path_t(id INT)")
		}
		s.Exec("ALTER TABLE ar_first.ar_path_t ENABLE ROW LEVEL SECURITY")
		s.Actor("ar_actor")
		path := predefinedHeld(s, "SELECT row_security_active($1::text) AS v")
		s.Exec("SET search_path TO ar_first,ar_second,public")
		fixture.AccessRoleHeldBool(s, path, true, "ar_path_t")
		s.Exec("SET search_path TO ar_second,ar_first,public")
		fixture.AccessRoleHeldBool(s, path, false, "ar_path_t")
		s.Exec("SET search_path TO public")
	})
}
