package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInfoRoutineRoutineUsageVisibility(t *testing.T) {
	rruScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, database string) {
		for _, role := range rruRoles {
			rruExec(t, p, b, c, "CREATE ROLE "+role)
		}
		rruExec(t, p, b, c, "ALTER ROLE rru_noinherit NOINHERIT",
			"CREATE SCHEMA rru_private", "CREATE SCHEMA rru_leaf_schema",
			"CREATE FUNCTION rru_leaf_schema.leaf() RETURNS integer RETURN 1",
			"CREATE FUNCTION rru_private.caller() RETURNS integer RETURN rru_leaf_schema.leaf()",
			"ALTER FUNCTION rru_private.caller() OWNER TO rru_caller_owner",
			"ALTER FUNCTION rru_leaf_schema.leaf() OWNER TO rru_leaf_owner",
			"REVOKE ALL ON FUNCTION rru_private.caller() FROM PUBLIC",
			"REVOKE ALL ON FUNCTION rru_leaf_schema.leaf() FROM PUBLIC",
			"GRANT rru_leaf_owner TO rru_middle WITH INHERIT TRUE",
			"GRANT rru_middle TO rru_member WITH INHERIT TRUE",
			"GRANT rru_leaf_owner TO rru_set WITH INHERIT FALSE, SET TRUE",
			"GRANT rru_leaf_owner TO rru_noinherit",
			"GRANT rru_middle TO rru_broken WITH INHERIT FALSE",
			"GRANT EXECUTE ON FUNCTION rru_leaf_schema.leaf() TO rru_reader,rru_caller_owner",
			"GRANT USAGE ON SCHEMA rru_leaf_schema,rru_private TO rru_caller_owner")
		want := [][]any{rruRow(database, "rru_private", rruID(t, p, c, "rru_private", "caller", ""), "rru_leaf_schema", rruID(t, p, c, "rru_leaf_schema", "leaf", ""))}
		s := rruProjection(b.PG, "specific_schema='rru_private'", want)
		held := rsuHeld(t, p, c, b.Recorder, s.Query)
		check := func(visible bool) {
			t.Helper()
			step := s
			if !visible {
				step = rruProjection(b.PG, "specific_schema='rru_private'", nil)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step))
			held(step)
		}
		check(true)
		for _, tc := range []struct {
			role    string
			visible bool
		}{{"rru_leaf_owner", true}, {"rru_member", true}, {"rru_reader", false}, {"rru_caller_owner", false}, {"rru_set", false}, {"rru_noinherit", false}, {"rru_broken", false}} {
			rruExec(t, p, b, c, "SET SESSION AUTHORIZATION "+tc.role)
			check(tc.visible)
			rruExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
		}
		rruExec(t, p, b, c, "SET SESSION AUTHORIZATION rru_set")
		check(false)
		rruExec(t, p, b, c, "SET ROLE rru_leaf_owner")
		check(true)
		rruExec(t, p, b, c, "RESET ROLE")
		check(false)
		rruExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "REVOKE rru_middle FROM rru_member", "SET SESSION AUTHORIZATION rru_member")
		check(false)
		rruExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT rru_middle TO rru_member WITH INHERIT TRUE", "SET SESSION AUTHORIZATION rru_member")
		check(true)
		rruExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT EXECUTE ON FUNCTION rru_leaf_schema.leaf() TO rru_member", "SET SESSION AUTHORIZATION rru_member")
		check(true)
		rruExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "REVOKE EXECUTE ON FUNCTION rru_leaf_schema.leaf() FROM rru_member", "ALTER FUNCTION rru_leaf_schema.leaf() OWNER TO rru_other", "SET SESSION AUTHORIZATION rru_member")
		check(false)
		rruExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT rru_other TO rru_member WITH INHERIT TRUE", "SET SESSION AUTHORIZATION rru_member")
		check(true)
		rruExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
	})
}
