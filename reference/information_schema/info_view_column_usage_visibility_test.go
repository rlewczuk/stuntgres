package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoViewColumnUsageVisibility(t *testing.T) {
	vcuScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, role := range vcuRoles {
			q := "CREATE ROLE " + role
			if role == "vcu_noinherit" {
				q += " NOINHERIT"
			}
			run(fixture.Exec(q, 0))
		}
		for _, q := range []string{
			"CREATE SCHEMA vcu_private", "REVOKE ALL ON SCHEMA vcu_private FROM PUBLIC",
			"CREATE TABLE vcu_private.b_t(a integer)", "CREATE TABLE vcu_private.c_t(a integer)",
			"CREATE VIEW vcu_private.v AS SELECT b.a FROM vcu_private.b_t b JOIN vcu_private.c_t c ON b.a=c.a",
			"ALTER TABLE vcu_private.b_t OWNER TO vcu_b", "ALTER TABLE vcu_private.c_t OWNER TO vcu_c", "ALTER VIEW vcu_private.v OWNER TO vcu_a",
			"REVOKE ALL ON vcu_private.v FROM PUBLIC",
			"GRANT SELECT ON vcu_private.v,vcu_private.b_t TO vcu_reader", "GRANT SELECT(a) ON vcu_private.c_t TO vcu_reader",
			"GRANT vcu_b TO vcu_set WITH INHERIT FALSE, SET TRUE",
			"GRANT vcu_b TO vcu_noinherit",
			"GRANT vcu_b TO vcu_middle WITH INHERIT TRUE, SET TRUE",
			"GRANT vcu_middle TO vcu_leaf WITH INHERIT TRUE, SET TRUE",
			"GRANT vcu_middle TO vcu_broken WITH INHERIT FALSE, SET TRUE",
		} {
			run(fixture.Exec(q, 0))
		}
		where := "view_schema='vcu_private' AND view_name='v'"
		bRow := vcuRow(database, "vcu_private", "v", "vcu_private", "b_t", "a")
		cRow := vcuRow(database, "vcu_private", "v", "vcu_private", "c_t", "a")
		visible := vcuProjection(b.PG, where, [][]any{bRow})
		hidden := vcuProjection(b.PG, where, nil)
		both := vcuProjection(b.PG, where, [][]any{bRow, cRow})
		run(both)
		run(fixture.Prepared(both))
		actor := func(role string, expected fixture.Step) {
			run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
			run(expected)
			run(fixture.Prepared(expected))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		for _, role := range []string{"vcu_b", "vcu_middle", "vcu_leaf"} {
			actor(role, visible)
		}
		actor("vcu_c", vcuProjection(b.PG, where, [][]any{cRow}))
		for _, role := range []string{"vcu_a", "vcu_reader", "vcu_set", "vcu_noinherit", "vcu_broken"} {
			actor(role, hidden)
		}
		// security_invoker is not an extra metadata disclosure predicate.
		run(fixture.Exec("ALTER VIEW vcu_private.v SET(security_invoker=true)", 0))
		actor("vcu_b", visible)
		actor("vcu_reader", hidden)
		admin, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer admin.Close()
		adminRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, admin, b.Recorder, s) }
		run(fixture.Exec("SET SESSION AUTHORIZATION vcu_reader", 0))
		held := vcuHeld(t, p, c, r, hidden.Query)
		held(hidden)
		adminRun(fixture.Exec("GRANT vcu_b TO vcu_reader WITH INHERIT TRUE, SET TRUE", 0))
		held(visible)
		adminRun(fixture.Exec("REVOKE vcu_b FROM vcu_reader", 0))
		held(hidden)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION vcu_set", 0))
		held(hidden)
		run(fixture.Exec("SET ROLE vcu_b", 0))
		held(visible)
		run(fixture.Exec("RESET ROLE", 0))
		held(hidden)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("ALTER VIEW vcu_private.v OWNER TO vcu_c", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION vcu_b", 0))
		held(visible) // Defining-view ownership does not matter.
		adminRun(fixture.Exec("ALTER TABLE vcu_private.b_t OWNER TO vcu_noinherit", 0))
		held(hidden)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION vcu_noinherit", 0))
		held(visible) // A NOINHERIT role still sees its own target attributes.
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}
