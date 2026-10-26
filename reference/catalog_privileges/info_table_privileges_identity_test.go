package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoTablePrivilegesVisibility(t *testing.T) {
	t.Run("public_origins", func(t *testing.T) {
		tpScenario(t, func(c *tpCase) {
			c.seed()
			c.run(c.conn, fixture.Exec("INSERT INTO tp_t VALUES(7)", 1))
			c.exec("SET ROLE tp_o", "GRANT SELECT ON tp_t TO PUBLIC", "RESET ROLE")
			public := c.row("tp_o", "PUBLIC", "public", "tp_t", "SELECT", "NO")
			c.actor("tp_p", func() {
				c.run(c.conn, fixture.Select("SELECT a FROM tp_t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
				c.check(c.conn, "tp_t", "PUBLIC", public)
				c.check(c.conn, "tp_t", "tp_o")
				s := c.step("SELECT * FROM information_schema.role_table_grants WHERE table_name=$1 AND grantee=$2", nil, "tp_t", "PUBLIC")
				c.run(c.conn, s)
				c.run(c.conn, fixture.Prepared(s))
			})
			c.actor("tp_o", func() { c.check(c.conn, "tp_t", "PUBLIC", public) })
			c.check(c.conn, "tp_t", "PUBLIC", public)
			c.exec("GRANT tp_o TO tp_m WITH INHERIT TRUE")
			c.actor("tp_m", func() { c.check(c.conn, "tp_t", "PUBLIC", public) })
			c.exec("GRANT tp_o TO tp_m WITH INHERIT FALSE, SET TRUE", "GRANT tp_power TO tp_m WITH INHERIT TRUE, SET FALSE")
			c.actor("tp_m", func() { c.check(c.conn, "tp_t", "PUBLIC", public); c.check(c.conn, "tp_t", "tp_o") })
			c.exec("SET ROLE tp_o", "GRANT SELECT ON tp_t TO tp_r", "RESET ROLE")
			c.actor("tp_r", func() {
				c.check(c.conn, "tp_t", "tp_r", c.r("SELECT", "NO"))
				c.check(c.conn, "tp_t", "PUBLIC", public)
			})
			c.actor("tp_p", func() { c.check(c.conn, "tp_t", "tp_r") })
			c.exec("SET ROLE tp_o", "REVOKE SELECT ON tp_t FROM PUBLIC", "RESET ROLE")
			for _, actor := range []string{"tp_p", "tp_r", "tp_o"} {
				c.actor(actor, func() { c.check(c.conn, "tp_t", "PUBLIC") })
			}
			c.actor("tp_r", func() { c.check(c.conn, "tp_t", "tp_r", c.r("SELECT", "NO")) })
		})
	})
	t.Run("schema_independence", func(t *testing.T) {
		tpScenario(t, func(c *tpCase) {
			c.seed()
			c.exec("CREATE SCHEMA tp_private AUTHORIZATION tp_o", "SET ROLE tp_o", "CREATE TABLE tp_private.tp_hidden(a integer)", "GRANT SELECT ON tp_private.tp_hidden TO tp_r", "RESET ROLE")
			c.exec("SET ROLE tp_o", "GRANT SELECT ON tp_private.tp_hidden TO PUBLIC", "RESET ROLE")
			public := c.row("tp_o", "PUBLIC", "tp_private", "tp_hidden", "SELECT", "NO")
			c.actor("tp_r", func() {
				c.check(c.conn, "tp_hidden", "tp_r", c.row("tp_o", "tp_r", "tp_private", "tp_hidden", "SELECT", "NO"))
				c.check(c.conn, "tp_hidden", "PUBLIC", public)
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError("SELECT a FROM tp_private.tp_hidden", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema tp_private", Position: fixture.Position(15)})
					s.SelectRows = true
					s.ErrorStage = "execute"
					if prepared {
						s = fixture.Prepared(s)
						s.ErrorStage = "prepare"
					}
					c.run(c.conn, s)
				}
			})
			c.actor("tp_p", func() { c.check(c.conn, "tp_hidden", "tp_r"); c.check(c.conn, "tp_hidden", "PUBLIC", public) })
			for _, change := range []string{"GRANT USAGE ON SCHEMA tp_private TO tp_r", "REVOKE USAGE ON SCHEMA tp_private FROM tp_r"} {
				c.exec(change)
				c.actor("tp_r", func() {
					c.exec("SET search_path=pg_catalog")
					c.check(c.conn, "tp_hidden", "PUBLIC", public)
					c.check(c.conn, "tp_hidden", "tp_r", c.row("tp_o", "tp_r", "tp_private", "tp_hidden", "SELECT", "NO"))
					c.exec("RESET search_path")
				})
			}
		})
	})
	t.Run("other_session_temp", func(t *testing.T) {
		tpScenario(t, func(c *tpCase) {
			c.seed()
			peer := c.peer()
			c.run(peer, fixture.Exec("SET ROLE tp_o", 0))
			c.run(peer, fixture.Exec("CREATE TEMP TABLE tp_temp(a integer)", 0))
			c.run(peer, fixture.Exec("GRANT SELECT ON tp_temp TO tp_r", 0))
			var schema string
			if err := peer.QueryRowContext(c.p.Ctx, "SELECT n.nspname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.oid='tp_temp'::regclass").Scan(&schema); err != nil {
				c.t.Fatal(err)
			}
			c.actor("tp_r", func() {
				c.check(c.conn, "tp_temp", "tp_r", c.row("tp_o", "tp_r", schema, "tp_temp", "SELECT", "NO"))
			})
			c.run(peer, fixture.Exec("DROP TABLE tp_temp", 0))
		})
	})
}

func TestReferenceInfoTablePrivilegesMembership(t *testing.T) {
	t.Run("paths_and_set_role", func(t *testing.T) {
		tpScenario(t, func(c *tpCase) {
			c.seed()
			c.exec("SET ROLE tp_o", "GRANT SELECT ON tp_t TO tp_g", "RESET ROLE", "GRANT tp_g TO tp_m WITH INHERIT FALSE, SET TRUE")
			row := c.row("tp_o", "tp_g", "public", "tp_t", "SELECT", "NO")
			held := c.hold(c.conn, tpQuery)
			read := func(want ...[]any) {
				c.actor("tp_m", func() { c.check(held, "tp_t", "tp_g", want...); c.check(c.conn, "tp_t", "tp_g", want...) })
			}
			read()
			c.actor("tp_m", func() {
				c.exec("SET ROLE tp_g")
				c.check(held, "tp_t", "tp_g", row)
				c.exec("RESET ROLE")
				c.check(held, "tp_t", "tp_g")
			})
			c.exec("GRANT tp_g TO tp_m WITH ADMIN TRUE, INHERIT FALSE, SET FALSE")
			read()
			c.exec("GRANT tp_x TO tp_m WITH INHERIT TRUE", "GRANT tp_y TO tp_m WITH INHERIT TRUE", "GRANT tp_g TO tp_x WITH INHERIT FALSE", "GRANT tp_g TO tp_y WITH INHERIT FALSE")
			read()
			c.exec("GRANT tp_g TO tp_x WITH INHERIT TRUE", "GRANT tp_g TO tp_y WITH INHERIT TRUE")
			read(row)
			c.exec("ALTER ROLE tp_m NOINHERIT")
			read(row)
			c.exec("REVOKE tp_g FROM tp_x")
			read(row)
			c.exec("REVOKE tp_g FROM tp_y")
			read()
			c.exec("SET ROLE tp_o", "GRANT UPDATE ON tp_t TO tp_m", "RESET ROLE")
			c.exec("GRANT tp_g TO tp_m WITH SET TRUE")
			c.actor("tp_m", func() {
				c.check(held, "tp_t", "tp_m", c.row("tp_o", "tp_m", "public", "tp_t", "UPDATE", "NO"))
				c.exec("SET ROLE tp_g")
				c.check(held, "tp_t", "tp_m")
				c.exec("RESET ROLE")
			})
			c.actor("tp_p", func() { c.check(held, "tp_t", "tp_g") })
			c.check(held, "tp_t", "tp_g", row)
		})
	})
	t.Run("drop_and_reuse", func(t *testing.T) {
		tpScenario(t, func(c *tpCase) {
			c.seed()
			c.exec("SET ROLE tp_o", "GRANT SELECT ON tp_t TO tp_g", "RESET ROLE", "GRANT tp_g TO tp_m WITH INHERIT TRUE")
			held := c.hold(c.conn, tpQuery)
			row := c.row("tp_o", "tp_g", "public", "tp_t", "SELECT", "NO")
			c.actor("tp_m", func() { c.check(held, "tp_t", "tp_g", row) })
			c.exec("SET ROLE tp_o", "REVOKE SELECT ON tp_t FROM tp_g", "RESET ROLE", "REVOKE tp_g FROM tp_m", "DROP ROLE tp_g", "CREATE ROLE tp_g")
			c.actor("tp_m", func() { c.check(held, "tp_t", "tp_g") })
			c.actor("tp_g", func() { c.check(held, "tp_t", "tp_g") })
			c.check(held, "tp_t", "tp_g")
			c.exec("GRANT tp_g TO tp_m WITH INHERIT TRUE")
			c.actor("tp_m", func() { c.check(held, "tp_t", "tp_g") })
			c.exec("SET ROLE tp_o", "GRANT SELECT ON tp_t TO tp_g", "RESET ROLE")
			c.actor("tp_m", func() { c.check(held, "tp_t", "tp_g", row) })
		})
	})
	t.Run("implicit_database_owner", func(t *testing.T) {
		tpScenario(t, func(c *tpCase) {
			c.exec("CREATE ROLE tp_o", "GRANT USAGE ON SCHEMA public TO tp_o", "CREATE TABLE public.tp_dbowner(a integer)", "ALTER TABLE public.tp_dbowner OWNER TO tp_o", "SET ROLE tp_o", "GRANT SELECT ON public.tp_dbowner TO pg_database_owner", "RESET ROLE")
			row := c.row("tp_o", "pg_database_owner", "public", "tp_dbowner", "SELECT", "NO")
			// Determine the real database owner independently; do not assume bootstrap identity.
			var owner string
			if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname=current_database()").Scan(&owner); err != nil {
				c.t.Fatal(err)
			}
			c.exec("CREATE ROLE tp_m", "GRANT "+owner+" TO tp_m WITH INHERIT TRUE")
			c.actor("tp_m", func() { c.check(c.conn, "tp_dbowner", "pg_database_owner", row) })
			c.exec("REVOKE " + owner + " FROM tp_m")
			c.actor("tp_m", func() { c.check(c.conn, "tp_dbowner", "pg_database_owner") })
		})
	})
}
