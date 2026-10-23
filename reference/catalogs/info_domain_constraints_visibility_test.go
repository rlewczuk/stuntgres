package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoDomainConstraintsVisibility(t *testing.T) {
	dcScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, role := range dcRoles {
			q := "CREATE ROLE " + role
			if role == "dc_noinherit" {
				q += " NOINHERIT"
			}
			run(fixture.Exec(q, 0))
		}
		for _, q := range []string{
			"CREATE SCHEMA dc_private", "REVOKE ALL ON SCHEMA dc_private FROM PUBLIC",
			"CREATE DOMAIN dc_private.secret AS integer CONSTRAINT secret_check CHECK(VALUE>0)",
			"CREATE DOMAIN dc_private.peer AS integer CONSTRAINT peer_check CHECK(VALUE>0)",
			"ALTER DOMAIN dc_private.secret OWNER TO dc_owner", "ALTER DOMAIN dc_private.peer OWNER TO dc_owner",
			"REVOKE ALL ON DOMAIN dc_private.secret,dc_private.peer FROM PUBLIC",
			"GRANT USAGE ON DOMAIN dc_private.peer TO dc_reader",
			"GRANT dc_owner TO dc_owner_member WITH INHERIT TRUE, SET TRUE",
			"GRANT dc_owner TO dc_noinherit WITH INHERIT FALSE, SET TRUE",
			"GRANT dc_owner TO dc_set WITH INHERIT FALSE, SET TRUE",
			"GRANT dc_usage_group TO dc_usage_member WITH INHERIT TRUE, SET TRUE",
		} {
			run(fixture.Exec(q, 0))
		}
		where := "domain_schema='dc_private' AND domain_name IN ('secret','peer')"
		secret := dcRow(database, "dc_private", "secret_check", "secret")
		peer := dcRow(database, "dc_private", "peer_check", "peer")
		held := dcHold(t, p, c, dcProjection(b.PG, where, nil).Query)
		check := func(want ...[]any) { dcCheck(t, p, c, r, held, dcProjection(b.PG, where, want)) }
		actor := func(role string, want ...[]any) {
			run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
			check(want...)
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		check(secret, peer) // The authenticated fixture administrator is a real superuser.
		actor("dc_owner", secret, peer)
		actor("dc_owner_member", secret, peer)
		actor("dc_noinherit")
		actor("dc_set")
		admin, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer admin.Close()
		adminRun := func(q string) { fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec(q, 0)) }
		run(fixture.Exec("SET SESSION AUTHORIZATION dc_reader", 0))
		check(peer)
		adminRun("GRANT USAGE ON SCHEMA dc_private TO dc_reader")
		check(peer)
		adminRun("REVOKE USAGE ON SCHEMA dc_private FROM dc_reader")
		adminRun("GRANT USAGE ON DOMAIN dc_private.secret TO dc_reader")
		check(secret, peer) // Type USAGE is sufficient without schema USAGE.
		adminRun("REVOKE USAGE ON DOMAIN dc_private.secret FROM dc_reader")
		check(peer)
		adminRun("GRANT USAGE ON DOMAIN dc_private.secret TO dc_reader WITH GRANT OPTION")
		check(secret, peer)
		adminRun("REVOKE ALL ON DOMAIN dc_private.secret FROM dc_reader")
		check(peer)
		adminRun("GRANT USAGE ON DOMAIN dc_private.secret TO PUBLIC")
		check(secret, peer)
		adminRun("REVOKE USAGE ON DOMAIN dc_private.secret FROM PUBLIC")
		check(peer)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		adminRun("GRANT USAGE ON DOMAIN dc_private.secret TO dc_usage_group")
		actor("dc_usage_member", secret)
		adminRun("REVOKE USAGE ON DOMAIN dc_private.secret FROM dc_usage_group")
		actor("dc_usage_member")
		run(fixture.Exec("SET SESSION AUTHORIZATION dc_set", 0))
		check()
		run(fixture.Exec("SET ROLE dc_owner", 0))
		check(secret, peer)
		run(fixture.Exec("RESET ROLE", 0))
		check()
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		// Ownership refresh on the same consumer, with no accidental PUBLIC right.
		run(fixture.Exec("SET SESSION AUTHORIZATION dc_owner", 0))
		check(secret, peer)
		adminRun("ALTER DOMAIN dc_private.secret OWNER TO dc_usage_group")
		check(peer)
		adminRun("ALTER DOMAIN dc_private.secret OWNER TO dc_owner")
		check(secret, peer)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}
