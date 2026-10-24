package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoRoleTableGrantsVisibility(t *testing.T) {
	t.Run("public_origins", func(t *testing.T) {
		rtgScenario(t, func(c *rtgCase) {
			c.seed()
			c.run(c.conn, fixture.Exec("INSERT INTO rtg_t VALUES(7)", 1))
			c.exec("SET ROLE rtg_o", "GRANT SELECT ON rtg_t TO PUBLIC", "RESET ROLE")
			public := c.row("rtg_o", "PUBLIC", "public", "rtg_t", "SELECT", "NO")
			c.actor("rtg_p", func() {
				c.run(c.conn, fixture.Select("SELECT a FROM rtg_t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
				c.check(c.conn, "rtg_t", "PUBLIC")
				c.check(c.conn, "rtg_t", "rtg_o")
			})
			c.actor("rtg_o", func() { c.check(c.conn, "rtg_t", "PUBLIC", public) })
			c.check(c.conn, "rtg_t", "PUBLIC", public)
			c.exec("GRANT rtg_o TO rtg_m WITH INHERIT TRUE")
			c.actor("rtg_m", func() { c.check(c.conn, "rtg_t", "PUBLIC", public) })
			c.exec("GRANT rtg_o TO rtg_m WITH INHERIT FALSE, SET TRUE", "GRANT rtg_power TO rtg_m WITH INHERIT TRUE, SET FALSE")
			c.actor("rtg_m", func() { c.check(c.conn, "rtg_t", "PUBLIC"); c.check(c.conn, "rtg_t", "rtg_o") })
			c.exec("SET ROLE rtg_o", "GRANT SELECT ON rtg_t TO rtg_r", "RESET ROLE")
			c.actor("rtg_r", func() { c.check(c.conn, "rtg_t", "rtg_r", c.r("SELECT", "NO")); c.check(c.conn, "rtg_t", "PUBLIC") })
			c.actor("rtg_p", func() { c.check(c.conn, "rtg_t", "rtg_r") })
		})
	})
	t.Run("schema_independence", func(t *testing.T) {
		rtgScenario(t, func(c *rtgCase) {
			c.seed()
			c.exec("CREATE SCHEMA rtg_private AUTHORIZATION rtg_o", "SET ROLE rtg_o", "CREATE TABLE rtg_private.rtg_hidden(a integer)", "GRANT SELECT ON rtg_private.rtg_hidden TO rtg_r", "RESET ROLE")
			c.actor("rtg_r", func() {
				c.check(c.conn, "rtg_hidden", "rtg_r", c.row("rtg_o", "rtg_r", "rtg_private", "rtg_hidden", "SELECT", "NO"))
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError("SELECT a FROM rtg_private.rtg_hidden", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema rtg_private", Position: fixture.Position(15)})
					s.SelectRows = true
					s.ErrorStage = "execute"
					if prepared {
						s = fixture.Prepared(s)
						s.ErrorStage = "prepare"
					}
					c.run(c.conn, s)
				}
			})
			c.actor("rtg_p", func() { c.check(c.conn, "rtg_hidden", "rtg_r") })
		})
	})
	t.Run("other_session_temp", func(t *testing.T) {
		rtgScenario(t, func(c *rtgCase) {
			c.seed()
			peer := c.peer()
			c.run(peer, fixture.Exec("SET ROLE rtg_o", 0))
			c.run(peer, fixture.Exec("CREATE TEMP TABLE rtg_temp(a integer)", 0))
			c.run(peer, fixture.Exec("GRANT SELECT ON rtg_temp TO rtg_r", 0))
			var schema string
			if err := peer.QueryRowContext(c.p.Ctx, "SELECT n.nspname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.oid='rtg_temp'::regclass").Scan(&schema); err != nil {
				c.t.Fatal(err)
			}
			c.actor("rtg_r", func() {
				c.check(c.conn, "rtg_temp", "rtg_r", c.row("rtg_o", "rtg_r", schema, "rtg_temp", "SELECT", "NO"))
			})
			c.run(peer, fixture.Exec("DROP TABLE rtg_temp", 0))
		})
	})
}

func TestReferenceInfoRoleTableGrantsMembership(t *testing.T) {
	t.Run("paths_and_set_role", func(t *testing.T) {
		rtgScenario(t, func(c *rtgCase) {
			c.seed()
			c.exec("SET ROLE rtg_o", "GRANT SELECT ON rtg_t TO rtg_g", "RESET ROLE", "GRANT rtg_g TO rtg_m WITH INHERIT FALSE, SET TRUE")
			row := c.row("rtg_o", "rtg_g", "public", "rtg_t", "SELECT", "NO")
			held := c.hold(c.conn, rtgQuery)
			read := func(want ...[]any) {
				c.actor("rtg_m", func() { c.check(held, "rtg_t", "rtg_g", want...); c.check(c.conn, "rtg_t", "rtg_g", want...) })
			}
			read()
			c.actor("rtg_m", func() {
				c.exec("SET ROLE rtg_g")
				c.check(held, "rtg_t", "rtg_g", row)
				c.exec("RESET ROLE")
				c.check(held, "rtg_t", "rtg_g")
			})
			c.exec("GRANT rtg_g TO rtg_m WITH ADMIN TRUE, INHERIT FALSE, SET FALSE")
			read()
			c.exec("GRANT rtg_x TO rtg_m WITH INHERIT TRUE", "GRANT rtg_y TO rtg_m WITH INHERIT TRUE", "GRANT rtg_g TO rtg_x WITH INHERIT FALSE", "GRANT rtg_g TO rtg_y WITH INHERIT FALSE")
			read()
			c.exec("GRANT rtg_g TO rtg_x WITH INHERIT TRUE", "GRANT rtg_g TO rtg_y WITH INHERIT TRUE")
			read(row)
			c.exec("ALTER ROLE rtg_m NOINHERIT")
			read(row)
			c.exec("REVOKE rtg_g FROM rtg_x")
			read(row)
			c.exec("REVOKE rtg_g FROM rtg_y")
			read()
			c.actor("rtg_p", func() { c.check(held, "rtg_t", "rtg_g") })
			c.check(held, "rtg_t", "rtg_g", row)
		})
	})
	t.Run("drop_and_reuse", func(t *testing.T) {
		rtgScenario(t, func(c *rtgCase) {
			c.seed()
			c.exec("SET ROLE rtg_o", "GRANT SELECT ON rtg_t TO rtg_g", "RESET ROLE", "GRANT rtg_g TO rtg_m WITH INHERIT TRUE")
			held := c.hold(c.conn, rtgQuery)
			row := c.row("rtg_o", "rtg_g", "public", "rtg_t", "SELECT", "NO")
			c.actor("rtg_m", func() { c.check(held, "rtg_t", "rtg_g", row) })
			c.exec("SET ROLE rtg_o", "REVOKE SELECT ON rtg_t FROM rtg_g", "RESET ROLE", "REVOKE rtg_g FROM rtg_m", "DROP ROLE rtg_g", "CREATE ROLE rtg_g")
			c.actor("rtg_m", func() { c.check(held, "rtg_t", "rtg_g") })
			c.actor("rtg_g", func() { c.check(held, "rtg_t", "rtg_g") })
			c.check(held, "rtg_t", "rtg_g")
			c.exec("GRANT rtg_g TO rtg_m WITH INHERIT TRUE")
			c.actor("rtg_m", func() { c.check(held, "rtg_t", "rtg_g") })
			c.exec("SET ROLE rtg_o", "GRANT SELECT ON rtg_t TO rtg_g", "RESET ROLE")
			c.actor("rtg_m", func() { c.check(held, "rtg_t", "rtg_g", row) })
		})
	})
	t.Run("implicit_database_owner", func(t *testing.T) {
		rtgScenario(t, func(c *rtgCase) {
			c.exec("CREATE ROLE rtg_o", "GRANT USAGE ON SCHEMA public TO rtg_o", "CREATE TABLE public.rtg_dbowner(a integer)", "ALTER TABLE public.rtg_dbowner OWNER TO rtg_o", "SET ROLE rtg_o", "GRANT SELECT ON public.rtg_dbowner TO pg_database_owner", "RESET ROLE")
			row := c.row("rtg_o", "pg_database_owner", "public", "rtg_dbowner", "SELECT", "NO")
			// Determine the real database owner independently; do not assume bootstrap identity.
			var owner string
			if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname=current_database()").Scan(&owner); err != nil {
				c.t.Fatal(err)
			}
			c.exec("CREATE ROLE rtg_m", "GRANT "+owner+" TO rtg_m WITH INHERIT TRUE")
			c.actor("rtg_m", func() { c.check(c.conn, "rtg_dbowner", "pg_database_owner", row) })
			c.exec("REVOKE " + owner + " FROM rtg_m")
			c.actor("rtg_m", func() { c.check(c.conn, "rtg_dbowner", "pg_database_owner") })
		})
	})
}
