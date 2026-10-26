package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoUserDefinedTypesVisibility(t *testing.T) {
	udtScenario(t, func(c *udtCase) {
		t := c.t
		for _, role := range udtRoles {
			q := "CREATE ROLE " + role
			if role == "udt_noinherit" {
				q += " NOINHERIT"
			}
			c.exec(q)
		}
		c.exec("CREATE SCHEMA udt_private", "REVOKE ALL ON SCHEMA udt_private FROM PUBLIC",
			"CREATE TYPE udt_private.secret AS (v integer)",
			"CREATE TYPE udt_private.peer AS (v integer)",
			"ALTER TYPE udt_private.secret OWNER TO udt_owner",
			"ALTER TYPE udt_private.peer OWNER TO udt_owner",
			"REVOKE ALL ON TYPE udt_private.secret,udt_private.peer FROM PUBLIC",
			"GRANT USAGE ON TYPE udt_private.peer TO udt_reader",
			"GRANT udt_owner TO udt_member WITH INHERIT TRUE, SET TRUE",
			"GRANT udt_owner TO udt_noinherit WITH INHERIT FALSE, SET TRUE",
			"GRANT udt_owner TO udt_set WITH INHERIT FALSE, SET TRUE",
			"GRANT udt_group TO udt_usage_member WITH INHERIT TRUE, SET TRUE")
		where := "user_defined_type_schema='udt_private' AND user_defined_type_name IN ('secret','peer')"
		h := c.hold(where)
		secret, peer := udtRow(c.database, "udt_private", "secret"), udtRow(c.database, "udt_private", "peer")
		check := func(want ...[]any) { c.held(h, where, nil, want...) }
		identity := func(current, session string) {
			c.run(fixture.Select("SELECT current_user,session_user", []string{"current_user", "session_user"}, []string{"NAME", "NAME"}, [][]any{{current, session}}))
		}
		actor := func(role string, want ...[]any) {
			c.exec("SET SESSION AUTHORIZATION " + role)
			identity(role, role)
			check(want...)
			c.exec("RESET SESSION AUTHORIZATION")
		}
		check(secret, peer)
		actor("udt_owner", secret, peer)
		actor("udt_member", secret, peer)
		actor("udt_noinherit")
		actor("udt_set")
		admin, err := c.b.DB.Conn(c.p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer admin.Close()
		adminExec := func(q string) { fixture.RunStep(t, c.p.Ctx, admin, c.b.Recorder, fixture.Exec(q, 0)) }
		c.exec("SET SESSION AUTHORIZATION udt_reader")
		check(peer)
		adminExec("GRANT USAGE ON SCHEMA udt_private TO udt_reader")
		check(peer)
		adminExec("REVOKE USAGE ON SCHEMA udt_private FROM udt_reader")
		adminExec("GRANT USAGE ON TYPE udt_private.secret TO udt_reader")
		check(secret, peer)
		adminExec("REVOKE USAGE ON TYPE udt_private.secret FROM udt_reader")
		check(peer)
		adminExec("GRANT USAGE ON TYPE udt_private.secret TO PUBLIC")
		check(secret, peer)
		adminExec("REVOKE USAGE ON TYPE udt_private.secret FROM PUBLIC")
		check(peer)
		c.exec("RESET SESSION AUTHORIZATION")
		adminExec("GRANT USAGE ON SCHEMA udt_private TO udt_group")
		actor("udt_group") // Schema USAGE alone is insufficient.
		adminExec("GRANT USAGE ON TYPE udt_private.secret TO udt_group")
		actor("udt_usage_member", secret)
		adminExec("REVOKE USAGE ON TYPE udt_private.secret FROM udt_group")
		actor("udt_usage_member")
		c.exec("SET SESSION AUTHORIZATION udt_set", "SET ROLE udt_owner")
		identity("udt_owner", "udt_set")
		check(secret, peer)
		c.exec("RESET ROLE")
		identity("udt_set", "udt_set")
		check()
		c.exec("RESET SESSION AUTHORIZATION", "SET SESSION AUTHORIZATION udt_owner")
		check(secret, peer)
		adminExec("ALTER TYPE udt_private.secret OWNER TO udt_group")
		check(peer)
		adminExec("ALTER TYPE udt_private.secret OWNER TO udt_owner")
		check(secret, peer)
		c.exec("RESET SESSION AUTHORIZATION")
		// Neither member-type authority nor relation SELECT gates a composite row.
		c.exec("CREATE DOMAIN udt_private.hidden_member AS integer", "REVOKE ALL ON TYPE udt_private.hidden_member FROM PUBLIC", "CREATE TYPE udt_private.child AS (v udt_private.hidden_member)", "REVOKE ALL ON TYPE udt_private.child FROM PUBLIC", "GRANT USAGE ON TYPE udt_private.child TO udt_reader", "SET SESSION AUTHORIZATION udt_reader")
		c.check("user_defined_type_schema='udt_private' AND user_defined_type_name IN ('hidden_member','child')", udtRow(c.database, "udt_private", "child"))
		c.exec("RESET SESSION AUTHORIZATION", "GRANT USAGE ON TYPE udt_private.secret TO udt_reader WITH GRANT OPTION")
		actor("udt_reader", secret, peer)
		c.exec("REVOKE USAGE ON TYPE udt_private.secret FROM udt_reader", "REVOKE ALL ON TYPE udt_private.secret FROM udt_owner")
		actor("udt_owner", secret, peer)
	})
}
