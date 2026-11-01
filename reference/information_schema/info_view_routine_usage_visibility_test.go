package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoViewRoutineUsageVisibility(t *testing.T) {
	vruScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, role := range vruRoles {
			q := "CREATE ROLE " + role
			if role == "vru_noinherit" {
				q += " NOINHERIT"
			}
			run(fixture.Exec(q, 0))
		}
		for _, q := range []string{
			"CREATE SCHEMA vru_private", "REVOKE ALL ON SCHEMA vru_private FROM PUBLIC",
			"CREATE FUNCTION vru_private.b_f() RETURNS integer RETURN 1",
			"CREATE FUNCTION vru_private.c_f() RETURNS integer RETURN 2",
			"CREATE VIEW vru_private.v AS SELECT vru_private.b_f()+vru_private.c_f() AS x",
			"ALTER FUNCTION vru_private.b_f() OWNER TO vru_b", "ALTER FUNCTION vru_private.c_f() OWNER TO vru_c",
			"ALTER VIEW vru_private.v OWNER TO vru_a", "REVOKE ALL ON vru_private.v FROM PUBLIC",
			"REVOKE ALL ON FUNCTION vru_private.b_f(),vru_private.c_f() FROM PUBLIC",
			"GRANT EXECUTE ON FUNCTION vru_private.b_f(),vru_private.c_f() TO vru_reader WITH GRANT OPTION",
			"GRANT vru_b TO vru_set WITH INHERIT FALSE,SET TRUE",
			"GRANT vru_b TO vru_noinherit",
			"GRANT vru_b TO vru_middle WITH INHERIT TRUE,SET TRUE",
			"GRANT vru_middle TO vru_leaf WITH INHERIT TRUE,SET TRUE",
			"GRANT vru_middle TO vru_broken WITH INHERIT FALSE,SET TRUE",
		} {
			run(fixture.Exec(q, 0))
		}
		where := "table_schema='vru_private'"
		bRow := vruRow(database, "vru_private", "v", "vru_private", rsuIdentity(t, p, c, "vru_private", "b_f", ""))
		cRow := vruRow(database, "vru_private", "v", "vru_private", rsuIdentity(t, p, c, "vru_private", "c_f", ""))
		visible := vruProjection(b.PG, where, [][]any{bRow})
		hidden := vruProjection(b.PG, where, nil)
		both := vruProjection(b.PG, where, [][]any{bRow, cRow})
		run(both)
		run(fixture.Prepared(both))
		actor := func(role string, s fixture.Step) {
			run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
			run(s)
			run(fixture.Prepared(s))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		for _, role := range []string{"vru_b", "vru_middle", "vru_leaf"} {
			actor(role, visible)
		}
		actor("vru_c", vruProjection(b.PG, where, [][]any{cRow}))
		for _, role := range []string{"vru_a", "vru_reader", "vru_set", "vru_noinherit", "vru_broken"} {
			actor(role, hidden)
		}
		run(fixture.Exec("ALTER VIEW vru_private.v SET(security_invoker=true)", 0))
		actor("vru_b", visible)
		actor("vru_reader", hidden)
		admin, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer admin.Close()
		adminRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, admin, b.Recorder, s) }
		run(fixture.Exec("SET SESSION AUTHORIZATION vru_reader", 0))
		held := vruHeld(t, p, c, r, hidden.Query)
		held(hidden)
		adminRun(fixture.Exec("GRANT vru_b TO vru_reader WITH INHERIT TRUE,SET TRUE", 0))
		held(visible)
		adminRun(fixture.Exec("REVOKE vru_b FROM vru_reader", 0))
		held(hidden)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION vru_set", 0))
		held(hidden)
		run(fixture.Exec("SET ROLE vru_b", 0))
		held(visible)
		run(fixture.Exec("RESET ROLE", 0))
		held(hidden)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("ALTER VIEW vru_private.v OWNER TO vru_c", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION vru_b", 0))
		held(visible)
		adminRun(fixture.Exec("ALTER FUNCTION vru_private.b_f() OWNER TO vru_noinherit", 0))
		held(hidden)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION vru_noinherit", 0))
		held(visible)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		// Identical unqualified names and overloads must not substitute for
		// the actual referenced routine's owner or specific identity.
		for _, q := range []string{
			"CREATE SCHEMA vru_other", "CREATE FUNCTION vru_other.b_f() RETURNS integer RETURN 3",
			"CREATE FUNCTION vru_other.b_f(integer) RETURNS integer RETURN $1",
			"ALTER FUNCTION vru_other.b_f() OWNER TO vru_c", "ALTER FUNCTION vru_other.b_f(integer) OWNER TO vru_b",
			"CREATE VIEW vru_other.v AS SELECT vru_other.b_f()+vru_other.b_f(1) AS x",
		} {
			run(fixture.Exec(q, 0))
		}
		other := vruRow(database, "vru_other", "v", "vru_other", rsuIdentity(t, p, c, "vru_other", "b_f", "integer"))
		run(fixture.Exec("SET search_path=vru_other,public", 0))
		actor("vru_b", vruProjection(b.PG, "table_schema IN ('vru_private','vru_other')", [][]any{other}))
		run(fixture.Exec("RESET search_path", 0))
	})
}
