package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoRoutinesVisibility(t *testing.T) {
	irScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, db string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		defer func() { _, _ = c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION") }()
		for _, q := range []string{
			"CREATE ROLE ir_owner", "CREATE ROLE ir_reader", "CREATE ROLE ir_inherit", "CREATE ROLE ir_noinherit NOINHERIT", "CREATE ROLE ir_set",
			"CREATE SCHEMA ir_private", "REVOKE ALL ON SCHEMA ir_private FROM PUBLIC",
			"CREATE FUNCTION ir_private.ir_over(integer) RETURNS integer LANGUAGE SQL AS ' SELECT $1 + 1 '",
			"CREATE FUNCTION ir_private.ir_over(text) RETURNS text LANGUAGE SQL AS ' SELECT $1 '",
			"ALTER FUNCTION ir_private.ir_over(integer) OWNER TO ir_owner", "ALTER FUNCTION ir_private.ir_over(text) OWNER TO ir_owner",
			"REVOKE ALL ON FUNCTION ir_private.ir_over(integer),ir_private.ir_over(text) FROM PUBLIC",
			"REVOKE ALL ON FUNCTION ir_private.ir_over(integer),ir_private.ir_over(text) FROM ir_owner",
			"GRANT ir_owner TO ir_inherit WITH INHERIT TRUE, SET TRUE", "GRANT ir_owner TO ir_noinherit", "GRANT ir_owner TO ir_set WITH INHERIT FALSE, SET TRUE",
		} {
			run(fixture.Exec(q, 0))
		}
		a := irRow(db, "ir_private", "ir_over", rsuIdentity(t, p, c, "ir_private", "ir_over", "integer"), "FUNCTION", "integer", "pg_catalog", "int4", "SQL", " SELECT $1 + 1 ", nil, false, false)
		z := irRow(db, "ir_private", "ir_over", rsuIdentity(t, p, c, "ir_private", "ir_over", "text"), "FUNCTION", "text", "pg_catalog", "text", "SQL", " SELECT $1 ", nil, false, false)
		where := "routine_schema='ir_private'"
		owner := irProjection(b.PG, where, [][]any{a, z})
		hidden := irProjection(b.PG, where, nil)
		redacted := append([]any(nil), a...)
		redacted[37] = nil
		execute := irProjection(b.PG, where, [][]any{redacted})
		held := rsuHeld(t, p, c, b.Recorder, hidden.Query)
		actor := func(role string, s fixture.Step) {
			run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
			irCheck(t, p, b, c, s)
			held(s)
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		irCheck(t, p, b, c, owner) // Superuser definition disclosure is independent of Execute.
		for _, role := range []string{"ir_owner", "ir_inherit"} {
			actor(role, owner)
		}
		for _, role := range []string{"ir_reader", "ir_noinherit", "ir_set"} {
			actor(role, hidden)
		}
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION ir_private.ir_over(integer) TO ir_reader", 0))
		actor("ir_reader", execute)
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION ir_private.ir_over(integer) FROM ir_reader", 0))
		actor("ir_reader", hidden)
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION ir_private.ir_over(integer) TO PUBLIC", 0))
		actor("ir_reader", execute)
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION ir_private.ir_over(integer) FROM PUBLIC", 0))
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION ir_private.ir_over(integer) TO ir_noinherit", 0))
		run(fixture.Exec("GRANT ir_noinherit TO ir_reader WITH INHERIT TRUE", 0))
		actor("ir_reader", execute)
		run(fixture.Exec("REVOKE ir_noinherit FROM ir_reader", 0))
		actor("ir_reader", hidden)
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION ir_private.ir_over(integer) FROM ir_noinherit", 0))
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION ir_private.ir_over(integer) TO ir_inherit", 0))
		run(fixture.Exec("GRANT ir_inherit TO ir_reader WITH INHERIT TRUE", 0))
		actor("ir_reader", owner)
		run(fixture.Exec("REVOKE ir_inherit FROM ir_reader", 0))
		actor("ir_reader", hidden)
		run(fixture.Exec("SET SESSION AUTHORIZATION ir_set", 0))
		held(hidden)
		run(fixture.Exec("SET ROLE ir_owner", 0))
		held(owner)
		run(fixture.Exec("RESET ROLE", 0))
		held(hidden)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		// C's symbol is public to Execute-only readers even when prosrc is redacted.
		id := rsuIdentity(t, p, c, "pg_catalog", "plpgsql_validator", "oid")
		external := irRow(db, "pg_catalog", "plpgsql_validator", id, "FUNCTION", "void", "pg_catalog", "void", "C", nil, "plpgsql_validator", false, true)
		actor("ir_reader", irProjection(b.PG, "specific_schema='pg_catalog' AND specific_name='"+id+"'", [][]any{external}))
	})
}
