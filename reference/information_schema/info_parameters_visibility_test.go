package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoParametersVisibility(t *testing.T) {
	ipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, db string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"CREATE ROLE ip_owner", "CREATE ROLE ip_reader", "CREATE ROLE ip_inherit", "CREATE ROLE ip_noinherit NOINHERIT", "CREATE ROLE ip_set",
			"CREATE SCHEMA ip_private", "REVOKE ALL ON SCHEMA ip_private FROM PUBLIC",
			"CREATE FUNCTION ip_private.ip_mix(IN a integer,OUT b text,INOUT c integer DEFAULT 9) LANGUAGE SQL AS 'SELECT a::text,c'",
			"ALTER FUNCTION ip_private.ip_mix(integer,integer) OWNER TO ip_owner",
			"REVOKE ALL ON FUNCTION ip_private.ip_mix(integer,integer) FROM PUBLIC",
			"REVOKE ALL ON FUNCTION ip_private.ip_mix(integer,integer) FROM ip_owner",
			"GRANT ip_owner TO ip_inherit WITH INHERIT TRUE, SET TRUE", "GRANT ip_owner TO ip_noinherit", "GRANT ip_owner TO ip_set WITH INHERIT FALSE, SET TRUE",
		} {
			run(fixture.Exec(q, 0))
		}
		id := rsuIdentity(t, p, c, "ip_private", "ip_mix", "integer, integer")
		owner := ipProjection(b.PG, ipIdentityWhere(id), ipMix(db, "ip_private", id, "9"))
		execute := ipProjection(b.PG, ipIdentityWhere(id), ipMix(db, "ip_private", id, nil))
		hidden := ipProjection(b.PG, ipIdentityWhere(id), nil)
		held := rsuHeld(t, p, c, b.Recorder, owner.Query)
		actor := func(role string, s fixture.Step) {
			run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
			irCheck(t, p, b, c, s)
			held(s)
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		irCheck(t, p, b, c, owner)
		actor("ip_owner", owner)
		actor("ip_inherit", owner)
		for _, role := range []string{"ip_reader", "ip_noinherit", "ip_set"} {
			actor(role, hidden)
		}
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION ip_private.ip_mix(integer,integer) TO ip_reader", 0))
		actor("ip_reader", execute)
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION ip_private.ip_mix(integer,integer) FROM ip_reader", 0))
		actor("ip_reader", hidden)
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION ip_private.ip_mix(integer,integer) TO PUBLIC", 0))
		actor("ip_reader", execute)
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION ip_private.ip_mix(integer,integer) FROM PUBLIC", 0))
		actor("ip_reader", hidden)
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION ip_private.ip_mix(integer,integer) TO ip_noinherit", 0))
		run(fixture.Exec("GRANT ip_noinherit TO ip_reader WITH INHERIT TRUE", 0))
		actor("ip_reader", execute)
		run(fixture.Exec("REVOKE ip_noinherit FROM ip_reader", 0))
		actor("ip_reader", hidden)
		actor("ip_noinherit", execute)
		run(fixture.Exec("SET SESSION AUTHORIZATION ip_set", 0))
		held(hidden)
		run(fixture.Exec("SET ROLE ip_owner", 0))
		held(owner)
		run(fixture.Exec("RESET ROLE", 0))
		held(hidden)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION ip_private.ip_mix(integer,integer) TO ip_set", 0))
		actor("ip_set", execute)
		// Type USAGE is not an additional information-schema admission requirement.
		run(fixture.Exec("CREATE DOMAIN ip_private.ip_domain AS integer", 0))
		run(fixture.Exec("CREATE FUNCTION ip_private.ip_typed(x ip_private.ip_domain) RETURNS integer LANGUAGE SQL AS 'SELECT 1'", 0))
		run(fixture.Exec("REVOKE ALL ON DOMAIN ip_private.ip_domain FROM PUBLIC", 0))
		run(fixture.Exec("REVOKE ALL ON FUNCTION ip_private.ip_typed(ip_private.ip_domain) FROM PUBLIC", 0))
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION ip_private.ip_typed(ip_private.ip_domain) TO ip_reader", 0))
		typed := rsuIdentity(t, p, c, "ip_private", "ip_typed", "ip_private.ip_domain")
		actor("ip_reader", ipProjection(b.PG, ipIdentityWhere(typed), [][]any{ipRow(db, "ip_private", typed, 1, "x", "IN", "USER-DEFINED", "ip_private", "ip_domain", nil)}))
		// PUBLIC EXECUTE exposes builtin formals but not the builtin owner's defaults.
		builtin := ipProjection(b.PG, "specific_schema='pg_catalog' AND specific_name='parse_ident_1268'", ipBuiltinDefaultRows(db, "parse_ident", 1268, nil))
		actor("ip_reader", builtin)
		irCheck(t, p, b, c, ipProjection(b.PG, "specific_schema='pg_catalog' AND specific_name='parse_ident_1268'", ipBuiltinDefaultRows(db, "parse_ident", 1268, map[int]string{2: "true"})))
	})
}
