package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoAttributesVisibility(t *testing.T) {
	attrScenario(t, func(c *attrCase) {
		t := c.t
		for _, role := range attrRoles {
			q := "CREATE ROLE " + role
			if role == "attr_noinherit" {
				q += " NOINHERIT"
			}
			c.exec(q)
		}
		c.exec("CREATE SCHEMA attr_private", "REVOKE ALL ON SCHEMA attr_private FROM PUBLIC",
			"CREATE TYPE attr_private.secret AS (id integer,label varchar(4))",
			"CREATE TYPE attr_private.peer AS (id integer,label varchar(4))",
			"ALTER TYPE attr_private.secret OWNER TO attr_owner", "ALTER TYPE attr_private.peer OWNER TO attr_owner",
			"REVOKE ALL ON TYPE attr_private.secret,attr_private.peer FROM PUBLIC",
			"GRANT USAGE ON TYPE attr_private.peer TO attr_reader",
			"GRANT attr_owner TO attr_member WITH INHERIT TRUE, SET TRUE",
			"GRANT attr_owner TO attr_noinherit WITH INHERIT FALSE, SET TRUE",
			"GRANT attr_owner TO attr_set WITH INHERIT FALSE, SET TRUE",
			"GRANT attr_group TO attr_usage_member WITH INHERIT TRUE, SET TRUE")
		rows := func(owner string) [][]any {
			return [][]any{
				attrPatch(attrRow(c.database, "attr_private", owner, "id", 1, "integer", "pg_catalog", "int4"), map[string]any{"numeric_precision": int64(32), "numeric_precision_radix": int64(2), "numeric_scale": int64(0)}),
				attrPatch(attrRow(c.database, "attr_private", owner, "label", 2, "character varying", "pg_catalog", "varchar"), map[string]any{"character_maximum_length": int64(4), "character_octet_length": int64(16)}),
			}
		}
		secret, peer := rows("secret"), rows("peer")
		both := append(append([][]any{}, peer...), secret...)
		where := "udt_schema='attr_private' AND udt_name IN ('secret','peer')"
		h := c.hold(where)
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
		check(both...)
		actor("attr_owner", both...)
		actor("attr_member", both...)
		actor("attr_noinherit")
		actor("attr_set")
		admin, err := c.b.DB.Conn(c.p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer admin.Close()
		adminExec := func(q string) { fixture.RunStep(t, c.p.Ctx, admin, c.b.Recorder, fixture.Exec(q, 0)) }
		c.exec("SET SESSION AUTHORIZATION attr_reader")
		readerCheck := func(want ...[]any) {
			identity("attr_reader", "attr_reader")
			check(want...)
		}
		readerCheck(peer...)
		adminExec("GRANT USAGE ON SCHEMA attr_private TO attr_reader")
		readerCheck(peer...)
		adminExec("REVOKE USAGE ON SCHEMA attr_private FROM attr_reader")
		adminExec("GRANT USAGE ON TYPE attr_private.secret TO attr_reader")
		readerCheck(both...)
		adminExec("REVOKE USAGE ON TYPE attr_private.secret FROM attr_reader")
		readerCheck(peer...)
		adminExec("GRANT USAGE ON TYPE attr_private.secret TO PUBLIC")
		readerCheck(both...)
		adminExec("REVOKE USAGE ON TYPE attr_private.secret FROM PUBLIC")
		readerCheck(peer...)
		c.exec("RESET SESSION AUTHORIZATION")
		adminExec("GRANT USAGE ON SCHEMA attr_private TO attr_group")
		actor("attr_group")
		adminExec("GRANT USAGE ON TYPE attr_private.secret TO attr_group")
		actor("attr_usage_member", secret...)
		adminExec("REVOKE USAGE ON TYPE attr_private.secret FROM attr_group")
		actor("attr_usage_member")
		c.exec("SET SESSION AUTHORIZATION attr_set", "SET ROLE attr_owner")
		identity("attr_owner", "attr_set")
		check(both...)
		c.exec("RESET ROLE")
		identity("attr_set", "attr_set")
		check()
		c.exec("RESET SESSION AUTHORIZATION", "SET SESSION AUTHORIZATION attr_owner")
		identity("attr_owner", "attr_owner")
		check(both...)
		adminExec("ALTER TYPE attr_private.secret OWNER TO attr_group")
		check(peer...)
		adminExec("ALTER TYPE attr_private.secret OWNER TO attr_owner")
		check(both...)
		c.exec("RESET SESSION AUTHORIZATION", "REVOKE ALL ON TYPE attr_private.secret FROM attr_owner")
		actor("attr_owner", both...)
		c.exec("CREATE DOMAIN attr_private.hidden_member AS integer", "REVOKE ALL ON TYPE attr_private.hidden_member FROM PUBLIC", "CREATE TYPE attr_private.child AS (v attr_private.hidden_member)", "REVOKE ALL ON TYPE attr_private.child FROM PUBLIC", "GRANT USAGE ON TYPE attr_private.child TO attr_reader", "SET SESSION AUTHORIZATION attr_reader")
		identity("attr_reader", "attr_reader")
		c.check("udt_schema='attr_private' AND udt_name IN ('hidden_member','child')", attrPatch(attrRow(c.database, "attr_private", "child", "v", 1, "USER-DEFINED", "attr_private", "hidden_member"), map[string]any{"numeric_precision": int64(32), "numeric_precision_radix": int64(2), "numeric_scale": int64(0)}))
		c.exec("RESET SESSION AUTHORIZATION")
	})
}
