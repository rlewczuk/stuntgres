package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoViewTableUsageVisibility(t *testing.T) {
	vtuScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, role := range vtuRoles {
			q := "CREATE ROLE " + role
			if role == "vtu_noinherit" {
				q += " NOINHERIT"
			}
			run(fixture.Exec(q, 0))
		}
		for _, q := range []string{
			"CREATE SCHEMA vtu_private", "REVOKE ALL ON SCHEMA vtu_private FROM PUBLIC",
			"CREATE TABLE vtu_private.b_t(a integer)", "CREATE TABLE vtu_private.c_t(a integer)",
			"CREATE VIEW vtu_private.v AS SELECT b.a FROM vtu_private.b_t b JOIN vtu_private.c_t c ON b.a=c.a",
			"ALTER TABLE vtu_private.b_t OWNER TO vtu_b", "ALTER TABLE vtu_private.c_t OWNER TO vtu_c", "ALTER VIEW vtu_private.v OWNER TO vtu_a",
			"REVOKE ALL ON vtu_private.v FROM PUBLIC",
			"GRANT SELECT ON vtu_private.v,vtu_private.b_t TO vtu_reader", "GRANT SELECT(a) ON vtu_private.c_t TO vtu_reader",
			"GRANT vtu_b TO vtu_set WITH INHERIT FALSE, SET TRUE",
			"GRANT vtu_b TO vtu_noinherit",
			"GRANT vtu_b TO vtu_middle WITH INHERIT TRUE, SET TRUE",
			"GRANT vtu_middle TO vtu_leaf WITH INHERIT TRUE, SET TRUE",
			"GRANT vtu_middle TO vtu_broken WITH INHERIT FALSE, SET TRUE",
		} {
			run(fixture.Exec(q, 0))
		}
		where := "view_schema='vtu_private' AND view_name='v'"
		bRow := vtuRow(database, "vtu_private", "v", "vtu_private", "b_t")
		cRow := vtuRow(database, "vtu_private", "v", "vtu_private", "c_t")
		visible := vtuProjection(b.PG, where, [][]any{bRow})
		hidden := vtuProjection(b.PG, where, nil)
		run(vtuProjection(b.PG, where, [][]any{bRow, cRow}))
		actor := func(role string, expected fixture.Step) {
			run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
			run(expected)
			run(fixture.Prepared(expected))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		for _, role := range []string{"vtu_b", "vtu_middle", "vtu_leaf"} {
			actor(role, visible)
		}
		actor("vtu_c", vtuProjection(b.PG, where, [][]any{cRow}))
		for _, role := range []string{"vtu_a", "vtu_reader", "vtu_set", "vtu_noinherit", "vtu_broken"} {
			actor(role, hidden)
		}
		// One physical held consumer observes authority changes, without view
		// SELECT or schema USAGE being an additional disclosure predicate.
		admin, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer admin.Close()
		adminRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, admin, b.Recorder, s) }
		run(fixture.Exec("SET SESSION AUTHORIZATION vtu_reader", 0))
		held := vtuHeld(t, p, c, r, hidden.Query)
		held(hidden)
		adminRun(fixture.Exec("GRANT vtu_b TO vtu_reader WITH INHERIT TRUE, SET TRUE", 0))
		held(visible)
		adminRun(fixture.Exec("REVOKE vtu_b FROM vtu_reader", 0))
		held(hidden)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION vtu_set", 0))
		held(hidden)
		run(fixture.Exec("SET ROLE vtu_b", 0))
		held(visible)
		run(fixture.Exec("RESET ROLE", 0))
		held(hidden)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("ALTER VIEW vtu_private.v OWNER TO vtu_c", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION vtu_b", 0))
		held(visible) // Changing only the defining owner changes nothing.
		adminRun(fixture.Exec("ALTER TABLE vtu_private.b_t OWNER TO vtu_noinherit", 0))
		held(hidden)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION vtu_noinherit", 0))
		held(visible) // NOINHERIT still includes the role's own objects.
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}
