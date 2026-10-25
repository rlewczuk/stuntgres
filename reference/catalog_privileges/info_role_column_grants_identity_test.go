package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoRoleColumnGrantsVisibility(t *testing.T) {
	t.Run("public_origins", func(t *testing.T) {
		rcgScenario(t, func(c *rcgCase) {
			c.seed()
			c.run(c.conn, fixture.Exec("INSERT INTO rcg_t VALUES(7,8)", 1))
			c.exec("SET ROLE rcg_o", "GRANT SELECT(a) ON rcg_t TO PUBLIC", "RESET ROLE")
			public := c.row("rcg_o", "PUBLIC", "public", "rcg_t", "a", "SELECT", "NO")
			c.actor("rcg_p", func() {
				c.run(c.conn, fixture.Select("SELECT a FROM rcg_t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
				c.check(c.conn, "rcg_t", "PUBLIC")
				c.check(c.conn, "rcg_t", "rcg_o")
			})
			c.actor("rcg_o", func() { c.check(c.conn, "rcg_t", "PUBLIC", public) })
			c.check(c.conn, "rcg_t", "PUBLIC", public)
			c.exec("GRANT rcg_o TO rcg_m WITH INHERIT TRUE")
			c.actor("rcg_m", func() { c.check(c.conn, "rcg_t", "PUBLIC", public) })
			c.exec("GRANT rcg_o TO rcg_m WITH INHERIT FALSE, SET TRUE", "GRANT rcg_power TO rcg_m WITH INHERIT TRUE, SET FALSE")
			c.actor("rcg_m", func() { c.check(c.conn, "rcg_t", "PUBLIC"); c.check(c.conn, "rcg_t", "rcg_o") })
		})
	})
	t.Run("schema_independence", func(t *testing.T) {
		rcgScenario(t, func(c *rcgCase) {
			c.seed()
			c.exec("CREATE SCHEMA rcg_private AUTHORIZATION rcg_o", "SET ROLE rcg_o", "CREATE TABLE rcg_private.rcg_hidden(a integer)", "GRANT SELECT(a) ON rcg_private.rcg_hidden TO rcg_p", "RESET ROLE")
			c.actor("rcg_p", func() {
				c.check(c.conn, "rcg_hidden", "rcg_p", c.row("rcg_o", "rcg_p", "rcg_private", "rcg_hidden", "a", "SELECT", "NO"))
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError("SELECT a FROM rcg_private.rcg_hidden", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema rcg_private", Position: fixture.Position(15)})
					s.SelectRows = true
					s.ErrorStage = "execute"
					if prepared {
						s = fixture.Prepared(s)
						s.ErrorStage = "prepare"
					}
					c.run(c.conn, s)
				}
			})
			c.actor("rcg_r", func() { c.check(c.conn, "rcg_hidden", "rcg_p") })
		})
	})
	t.Run("other_session_temp", func(t *testing.T) {
		rcgScenario(t, func(c *rcgCase) {
			c.seed()
			peer := c.peer()
			c.run(peer, fixture.Exec("SET ROLE rcg_o", 0))
			c.run(peer, fixture.Exec("CREATE TEMP TABLE rcg_temp(a integer)", 0))
			c.run(peer, fixture.Exec("GRANT SELECT(a) ON rcg_temp TO rcg_p", 0))
			var schema string
			if err := peer.QueryRowContext(c.p.Ctx, "SELECT n.nspname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.oid='rcg_temp'::regclass").Scan(&schema); err != nil {
				c.t.Fatal(err)
			}
			c.actor("rcg_p", func() {
				c.check(c.conn, "rcg_temp", "rcg_p", c.row("rcg_o", "rcg_p", schema, "rcg_temp", "a", "SELECT", "NO"))
			})
			c.run(peer, fixture.Exec("DROP TABLE rcg_temp", 0))
		})
	})
}

func TestReferenceInfoRoleColumnGrantsMembership(t *testing.T) {
	t.Run("paths_and_set_role", func(t *testing.T) {
		rcgScenario(t, func(c *rcgCase) {
			c.seed()
			c.exec("SET ROLE rcg_o", "GRANT SELECT(a) ON rcg_t TO rcg_g", "RESET ROLE", "GRANT rcg_g TO rcg_m WITH INHERIT FALSE, SET TRUE")
			row := c.row("rcg_o", "rcg_g", "public", "rcg_t", "a", "SELECT", "NO")
			held := c.hold(c.conn, rcgQuery)
			read := func(want ...[]any) { c.actor("rcg_m", func() { c.check(held, "rcg_t", "rcg_g", want...) }) }
			read()
			c.actor("rcg_m", func() {
				c.exec("SET ROLE rcg_g")
				c.check(held, "rcg_t", "rcg_g", row)
				c.exec("RESET ROLE")
				c.check(held, "rcg_t", "rcg_g")
			})
			c.exec("GRANT rcg_g TO rcg_m WITH ADMIN TRUE, INHERIT FALSE, SET FALSE")
			read()
			c.exec("GRANT rcg_x TO rcg_m WITH INHERIT TRUE", "GRANT rcg_y TO rcg_m WITH INHERIT TRUE", "GRANT rcg_g TO rcg_x WITH INHERIT TRUE", "GRANT rcg_g TO rcg_y WITH INHERIT TRUE")
			read(row)
			c.exec("REVOKE rcg_g FROM rcg_x")
			read(row)
			c.exec("REVOKE rcg_g FROM rcg_y")
			read()
			c.actor("rcg_p", func() { c.check(held, "rcg_t", "rcg_g") })
			c.check(held, "rcg_t", "rcg_g", row)
		})
	})
	t.Run("drop_and_reuse", func(t *testing.T) {
		rcgScenario(t, func(c *rcgCase) {
			c.seed()
			c.exec("SET ROLE rcg_o", "GRANT SELECT(a) ON rcg_t TO rcg_g", "RESET ROLE", "GRANT rcg_g TO rcg_m WITH INHERIT TRUE")
			held := c.hold(c.conn, rcgQuery)
			row := c.row("rcg_o", "rcg_g", "public", "rcg_t", "a", "SELECT", "NO")
			c.actor("rcg_m", func() { c.check(held, "rcg_t", "rcg_g", row) })
			c.exec("SET ROLE rcg_o", "REVOKE SELECT(a) ON rcg_t FROM rcg_g", "RESET ROLE", "REVOKE rcg_g FROM rcg_m", "DROP ROLE rcg_g", "CREATE ROLE rcg_g")
			c.actor("rcg_m", func() { c.check(held, "rcg_t", "rcg_g") })
			c.actor("rcg_g", func() { c.check(held, "rcg_t", "rcg_g") })
			c.check(held, "rcg_t", "rcg_g")
			c.exec("GRANT rcg_g TO rcg_m WITH INHERIT TRUE")
			c.actor("rcg_m", func() { c.check(held, "rcg_t", "rcg_g") })
			c.exec("SET ROLE rcg_o", "GRANT SELECT(a) ON rcg_t TO rcg_g", "RESET ROLE")
			c.actor("rcg_m", func() { c.check(held, "rcg_t", "rcg_g", row) })
		})
	})
}
