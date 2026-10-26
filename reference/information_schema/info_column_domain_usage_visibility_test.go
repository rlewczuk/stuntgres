package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoColumnDomainUsageVisibility(t *testing.T) {
	cduScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, role := range cduRoles {
			q := "CREATE ROLE " + role
			if role == "cdu_noinherit" {
				q += " NOINHERIT"
			}
			run(fixture.Exec(q, 0))
		}
		cduSeed(run)
		for _, q := range []string{
			"ALTER DOMAIN cdu.parent OWNER TO cdu_owner", "ALTER DOMAIN cdu.child OWNER TO cdu_owner",
			"ALTER TABLE cdu.t OWNER TO cdu_table_owner", "ALTER VIEW cdu.v OWNER TO cdu_table_owner",
			"REVOKE ALL ON SCHEMA cdu FROM PUBLIC", "REVOKE ALL ON DOMAIN cdu.parent,cdu.child FROM PUBLIC",
			"GRANT cdu_owner TO cdu_member WITH INHERIT TRUE, SET TRUE",
			"GRANT cdu_owner TO cdu_noinherit WITH INHERIT FALSE, SET TRUE",
			"GRANT cdu_owner TO cdu_set WITH INHERIT FALSE, SET TRUE",
		} {
			run(fixture.Exec(q, 0))
		}
		where := "table_schema='cdu'"
		want := cduSeedRows(database)
		held := cduHold(t, p, c, cduProjection(b.PG, where, nil).Query)
		check := func(rows [][]any) { cduCheck(t, p, c, r, held, cduProjection(b.PG, where, rows)) }
		actor := func(role string, rows [][]any) {
			run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
			check(rows)
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		check(want)
		actor("cdu_owner", want)
		actor("cdu_member", want)
		for _, role := range []string{"cdu_reader", "cdu_table_owner", "cdu_noinherit", "cdu_set"} {
			actor(role, nil)
		}
		admin, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer admin.Close()
		adminRun := func(q string) { fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec(q, 0)) }
		run(fixture.Exec("SET SESSION AUTHORIZATION cdu_reader", 0))
		for _, q := range []string{
			"GRANT USAGE ON SCHEMA cdu TO cdu_reader",
			"GRANT SELECT(a) ON cdu.t TO cdu_reader",
			"GRANT SELECT ON cdu.t,cdu.v TO cdu_reader",
			"GRANT USAGE ON DOMAIN cdu.parent,cdu.child TO cdu_reader",
			"GRANT USAGE ON DOMAIN cdu.parent,cdu.child TO cdu_reader WITH GRANT OPTION",
			"GRANT USAGE ON DOMAIN cdu.parent,cdu.child TO PUBLIC",
		} {
			adminRun(q)
			check(nil)
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		// Enabled ownership, unlike ACL USAGE, survives denial of all rights
		// on the referenced namespaces and tables.
		adminRun("REVOKE ALL ON DOMAIN cdu.parent,cdu.child FROM PUBLIC,cdu_reader")
		actor("cdu_owner", want)
		for _, role := range []string{"cdu_noinherit", "cdu_set"} {
			run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
			check(nil)
			run(fixture.Exec("SET ROLE cdu_owner", 0))
			check(want)
			run(fixture.Exec("RESET ROLE", 0))
			check(nil)
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION cdu_member", 0))
		check(want)
		adminRun("GRANT cdu_owner TO cdu_member WITH INHERIT FALSE")
		check(nil)
		adminRun("GRANT cdu_owner TO cdu_member WITH INHERIT TRUE")
		check(want)
		adminRun("REVOKE cdu_owner FROM cdu_member")
		check(nil)
		adminRun("GRANT cdu_owner TO cdu_member WITH INHERIT TRUE, SET TRUE")
		check(want)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION cdu_owner", 0))
		adminRun("ALTER DOMAIN cdu.child OWNER TO cdu_reader")
		check([][]any{want[0], want[2]})
		adminRun("ALTER DOMAIN cdu.child OWNER TO cdu_owner")
		check(want)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}
