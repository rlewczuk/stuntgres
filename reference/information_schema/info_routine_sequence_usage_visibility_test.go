package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoRoutineSequenceUsageVisibility(t *testing.T) {
	rsuScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, role := range rsuRoles {
			q := "CREATE ROLE " + role
			if role == "rsu_noinherit" {
				q += " NOINHERIT"
			}
			run(fixture.Exec(q, 0))
		}
		for _, q := range []string{
			"CREATE SCHEMA rsu_private", "CREATE SEQUENCE rsu_private.s",
			"CREATE FUNCTION rsu_private.r() RETURNS bigint RETURN nextval('rsu_private.s')",
			"REVOKE ALL ON SCHEMA rsu_private FROM PUBLIC",
			"REVOKE ALL ON FUNCTION rsu_private.r() FROM PUBLIC",
			"ALTER SEQUENCE rsu_private.s OWNER TO rsu_owner",
			"GRANT USAGE, SELECT ON SEQUENCE rsu_private.s TO rsu_reader",
			"GRANT rsu_owner TO rsu_set WITH INHERIT FALSE, SET TRUE",
			"GRANT rsu_owner TO rsu_noinherit",
			"GRANT rsu_owner TO rsu_middle WITH INHERIT TRUE, SET TRUE",
			"GRANT rsu_middle TO rsu_leaf WITH INHERIT TRUE, SET TRUE",
			"GRANT rsu_middle TO rsu_broken WITH INHERIT FALSE, SET TRUE",
		} {
			run(fixture.Exec(q, 0))
		}
		id := rsuIdentity(t, p, c, "rsu_private", "r", "")
		want := [][]any{rsuRow(database, "rsu_private", "r", id, "rsu_private", "s")}
		where := "routine_schema='rsu_private'"
		visible := rsuProjection(b.PG, where, want)
		hidden := rsuProjection(b.PG, where, nil)
		run(visible) // Superuser sees all despite the distinct routine owner.
		actor := func(role string, expected fixture.Step) {
			run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
			run(expected)
			run(fixture.Prepared(expected))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		for _, role := range []string{"rsu_owner", "rsu_middle", "rsu_leaf"} {
			actor(role, visible)
		}
		for _, role := range []string{"rsu_reader", "rsu_set", "rsu_noinherit", "rsu_broken"} {
			actor(role, hidden)
		}
		// The view's predicate is enabled sequence-owner roles, NOT sequence
		// ACLs, routine EXECUTE, schema USAGE, or routine ownership.
		admin, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer admin.Close()
		adminRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, admin, b.Recorder, s) }
		run(fixture.Exec("SET SESSION AUTHORIZATION rsu_reader", 0))
		held := rsuHeld(t, p, c, r, hidden.Query)
		held(hidden)
		adminRun(fixture.Exec("GRANT rsu_owner TO rsu_reader WITH INHERIT TRUE, SET TRUE", 0))
		held(visible)
		adminRun(fixture.Exec("REVOKE rsu_owner FROM rsu_reader", 0))
		held(hidden)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rsu_set", 0))
		held(hidden)
		run(fixture.Exec("SET ROLE rsu_owner", 0))
		held(visible)
		run(fixture.Exec("RESET ROLE", 0))
		held(hidden)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("ALTER SEQUENCE rsu_private.s OWNER TO rsu_noinherit", 0))
		actor("rsu_noinherit", visible) // NOINHERIT does not hide self ownership.
		actor("rsu_owner", hidden)
	})
}
