package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInfoUserMappingOptionsVisibility(t *testing.T) {
	umoScenario(t, func(c *umoCase) {
		c.seed()
		c.ownership()
		c.exec("CREATE SERVER umo_hidden FOREIGN DATA WRAPPER umo_fdw",
			"CREATE USER MAPPING FOR umo_alice SERVER umo_hidden OPTIONS(password 'hidden-secret')",
			"GRANT umo_owner TO umo_owner_member WITH INHERIT TRUE, SET TRUE",
			"GRANT umo_owner TO umo_owner_set WITH INHERIT FALSE, SET TRUE",
			"GRANT umo_reader TO umo_usage_member WITH INHERIT TRUE, SET TRUE",
			"GRANT umo_alice TO umo_alice_member WITH INHERIT TRUE, SET TRUE")
		_, held := c.hold()
		c.check(c.rows("all"))
		c.routes(umoQuery, [][]any{c.row("umo_alice", "umo_hidden", "password", "hidden-secret")}, "umo_hidden")
		cases := []struct {
			role, disclose string
			visible        bool
		}{
			{"umo_alice", "umo_alice", true}, {"umo_reader", "none", true}, {"umo_usage_member", "none", true},
			{"umo_owner", "PUBLIC", true}, {"umo_owner_member", "PUBLIC", true}, {"umo_alice_member", "none", true},
			{"umo_bob", "", false}, {"umo_denied", "", false}, {"umo_owner_set", "", false},
		}
		for _, tc := range cases {
			c.exec("SET SESSION AUTHORIZATION " + tc.role)
			var want [][]any
			if tc.visible {
				want = c.rows(tc.disclose)
			}
			c.check(want)
			held(c.selectStep(umoQuery, want, "umo_server"))
			c.routes(umoQuery, nil, "umo_hidden")
			held(c.selectStep(umoQuery, nil, "umo_hidden"))
			c.exec("RESET SESSION AUTHORIZATION")
		}
		for _, tc := range []struct{ session, role, disclose string }{{"umo_owner_set", "umo_owner", "PUBLIC"}, {"umo_alice_member", "umo_alice", "umo_alice"}} {
			c.exec("SET SESSION AUTHORIZATION "+tc.session, "SET ROLE "+tc.role)
			c.check(c.rows(tc.disclose))
			held(c.selectStep(umoQuery, c.rows(tc.disclose), "umo_server"))
			c.exec("RESET ROLE", "RESET SESSION AUTHORIZATION")
		}
		c.exec("GRANT USAGE ON FOREIGN SERVER umo_server TO PUBLIC")
		c.actor("umo_bob", c.rows("umo_bob"))
		c.exec("REVOKE USAGE ON FOREIGN SERVER umo_server FROM PUBLIC")
		c.actor("umo_bob", nil)
		c.exec("REVOKE USAGE ON FOREIGN DATA WRAPPER umo_fdw FROM umo_owner")
		c.actor("umo_reader", c.rows("none"))
		c.actor("umo_owner", c.rows("PUBLIC"))
		c.exec("GRANT USAGE ON FOREIGN SERVER umo_hidden TO umo_alice", "SET SESSION AUTHORIZATION umo_alice")
		c.routes(umoQuery, [][]any{c.row("umo_alice", "umo_hidden", "password", "hidden-secret")}, "umo_hidden")
		c.exec("RESET SESSION AUTHORIZATION")
	})
}

func TestReferenceInfoUserMappingOptionsRefresh(t *testing.T) {
	umoScenario(t, func(c *umoCase) {
		c.seed()
		c.ownership()
		admin, err := c.b.DB.Conn(c.p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer admin.Close()
		adminExec := func(q string) { fixture.RunStep(t, c.p.Ctx, admin, c.b.Recorder, fixture.Exec(q, 0)) }
		stmt, held := c.hold()
		defer stmt.Close()
		check := func(want [][]any) { held(c.selectStep(umoQuery, want, "umo_server")) }
		c.exec("SET SESSION AUTHORIZATION umo_bob")
		check(nil)
		adminExec("GRANT USAGE ON FOREIGN SERVER umo_server TO umo_bob")
		check(c.rows("umo_bob"))
		adminExec("REVOKE USAGE ON FOREIGN SERVER umo_server FROM umo_bob")
		check(nil)
		c.exec("RESET SESSION AUTHORIZATION")
		for _, tc := range []struct{ role, disclose string }{{"umo_alice", "umo_alice"}, {"umo_reader", "none"}, {"umo_owner", "PUBLIC"}} {
			c.exec("SET SESSION AUTHORIZATION " + tc.role)
			check(c.rows(tc.disclose))
			c.exec("RESET SESSION AUTHORIZATION")
		}
		check(c.rows("all"))
		c.exec("SET SESSION AUTHORIZATION umo_denied")
		check(nil)
		adminExec("GRANT umo_reader TO umo_denied WITH INHERIT TRUE, SET TRUE")
		check(c.rows("none"))
		adminExec("REVOKE umo_reader FROM umo_denied")
		check(nil)
		adminExec("GRANT umo_owner TO umo_denied WITH INHERIT TRUE, SET TRUE")
		check(c.rows("PUBLIC"))
		adminExec("REVOKE umo_owner FROM umo_denied")
		check(nil)
		adminExec("GRANT umo_owner TO umo_denied WITH INHERIT FALSE, SET TRUE")
		check(nil)
		c.exec("SET ROLE umo_owner")
		check(c.rows("PUBLIC"))
		c.exec("RESET ROLE")
		check(nil)
		c.exec("RESET SESSION AUTHORIZATION")
		if !c.b.PG {
			rows, err := c.conn.QueryContext(c.p.Ctx, umoQuery, "umo_server")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			adminExec("ALTER USER MAPPING FOR umo_alice SERVER umo_server OPTIONS(SET password 'changed full=é')")
			c.consume(rows, c.rows("all"))
		} else {
			adminExec("ALTER USER MAPPING FOR umo_alice SERVER umo_server OPTIONS(SET password 'changed full=é')")
		}
		c.exec("SET SESSION AUTHORIZATION umo_reader")
		check(c.rows("none"))
		c.exec("RESET SESSION AUTHORIZATION")
		changed := c.rows("umo_alice")
		changed[2][4] = "changed full=é"
		c.exec("SET SESSION AUTHORIZATION umo_alice")
		check(changed)
		c.exec("RESET SESSION AUTHORIZATION")
	})
}
