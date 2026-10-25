package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoDomainsVisibility(t *testing.T) {
	idmScenario(t, func(c *idmCase) {
		t := c.t
		for _, role := range idmRoles {
			q := "CREATE ROLE " + role
			if role == "idm_noinherit" {
				q += " NOINHERIT"
			}
			c.exec(q)
		}
		c.exec("CREATE SCHEMA idm_private", "REVOKE ALL ON SCHEMA idm_private FROM PUBLIC",
			"CREATE DOMAIN idm_private.secret AS integer",
			"CREATE DOMAIN idm_private.peer AS integer",
			"ALTER DOMAIN idm_private.secret OWNER TO idm_owner",
			"ALTER DOMAIN idm_private.peer OWNER TO idm_owner",
			"REVOKE ALL ON DOMAIN idm_private.secret,idm_private.peer FROM PUBLIC",
			"GRANT USAGE ON DOMAIN idm_private.peer TO idm_reader",
			"GRANT idm_owner TO idm_member WITH INHERIT TRUE, SET TRUE",
			"GRANT idm_owner TO idm_noinherit WITH INHERIT FALSE, SET TRUE",
			"GRANT idm_owner TO idm_set WITH INHERIT FALSE, SET TRUE",
			"GRANT idm_group TO idm_usage_member WITH INHERIT TRUE, SET TRUE")
		where := "domain_schema='idm_private' AND domain_name IN ('secret','peer')"
		h := c.hold(where)
		secret, peer := idmInteger(c.database, "idm_private", "secret", nil), idmInteger(c.database, "idm_private", "peer", nil)
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
		actor("idm_owner", secret, peer)
		actor("idm_member", secret, peer)
		actor("idm_noinherit")
		actor("idm_set")
		admin, err := c.b.DB.Conn(c.p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer admin.Close()
		adminExec := func(q string) { fixture.RunStep(t, c.p.Ctx, admin, c.b.Recorder, fixture.Exec(q, 0)) }
		c.exec("SET SESSION AUTHORIZATION idm_reader")
		check(peer)
		adminExec("GRANT USAGE ON SCHEMA idm_private TO idm_reader")
		check(peer)
		adminExec("REVOKE USAGE ON SCHEMA idm_private FROM idm_reader")
		adminExec("GRANT USAGE ON DOMAIN idm_private.secret TO idm_reader")
		check(secret, peer)
		adminExec("REVOKE USAGE ON DOMAIN idm_private.secret FROM idm_reader")
		check(peer)
		adminExec("GRANT USAGE ON DOMAIN idm_private.secret TO PUBLIC")
		check(secret, peer)
		adminExec("REVOKE USAGE ON DOMAIN idm_private.secret FROM PUBLIC")
		check(peer)
		c.exec("RESET SESSION AUTHORIZATION")
		adminExec("GRANT USAGE ON SCHEMA idm_private TO idm_group")
		actor("idm_group") // Schema USAGE alone is insufficient.
		adminExec("GRANT USAGE ON DOMAIN idm_private.secret TO idm_group")
		actor("idm_usage_member", secret)
		adminExec("REVOKE USAGE ON DOMAIN idm_private.secret FROM idm_group")
		actor("idm_usage_member")
		c.exec("SET SESSION AUTHORIZATION idm_set", "SET ROLE idm_owner")
		identity("idm_owner", "idm_set")
		check(secret, peer)
		c.exec("RESET ROLE")
		identity("idm_set", "idm_set")
		check()
		c.exec("RESET SESSION AUTHORIZATION", "SET SESSION AUTHORIZATION idm_owner")
		check(secret, peer)
		adminExec("ALTER DOMAIN idm_private.secret OWNER TO idm_group")
		check(peer)
		adminExec("ALTER DOMAIN idm_private.secret OWNER TO idm_owner")
		check(secret, peer)
		c.exec("RESET SESSION AUTHORIZATION")
		// A visible child's immediate base identity is not separately filtered.
		c.exec("CREATE DOMAIN idm_private.child AS idm_private.secret",
			"REVOKE ALL ON DOMAIN idm_private.child FROM PUBLIC",
			"GRANT USAGE ON DOMAIN idm_private.child TO idm_reader",
			"SET SESSION AUTHORIZATION idm_reader")
		c.check("domain_schema='idm_private' AND domain_name IN ('secret','child')",
			idmRow(c.database, "idm_private", "child", "USER-DEFINED", "idm_private", "secret", nil))
		c.exec("RESET SESSION AUTHORIZATION")
	})
}
