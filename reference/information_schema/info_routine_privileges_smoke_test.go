package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoRoutinePrivilegesFeatureSmoke(t *testing.T) {
	rpScenario(t, func(c *rrgCase) {
		c.exec("CREATE ROLE rp_smoke_o", "CREATE ROLE rp_smoke_r", "CREATE ROLE rp_smoke_x", "CREATE FUNCTION public.rp_smoke() RETURNS integer RETURN 7", "ALTER FUNCTION public.rp_smoke() OWNER TO rp_smoke_o")
		c.exec("GRANT USAGE ON SCHEMA public TO rp_smoke_o")
		schema, name, _, specific, _ := c.identity("public.rp_smoke()")
		origin := func(grantee, option string) []any {
			return c.row("rp_smoke_o", grantee, schema, name, specific, option)
		}
		peer := c.peer()
		c.run(peer, fixture.Exec("SET ROLE rp_smoke_o", 0))
		c.exec("SET SESSION AUTHORIZATION rp_smoke_x")
		held := c.hold(c.conn, rpQuery)
		read := func(want ...[]any) { c.check(held, schema, name, want...) }
		read(origin("PUBLIC", "NO"))
		s := c.step(rrgQuery, nil, schema, name)
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
		c.run(peer, fixture.Exec("REVOKE EXECUTE ON FUNCTION public.rp_smoke() FROM PUBLIC", 0))
		read()
		c.run(peer, fixture.Exec("GRANT EXECUTE ON FUNCTION public.rp_smoke() TO rp_smoke_r", 0))
		c.exec("RESET SESSION AUTHORIZATION", "SET SESSION AUTHORIZATION rp_smoke_r")
		read(origin("rp_smoke_r", "NO"))
		for _, phase := range []struct{ query, option string }{
			{"GRANT EXECUTE ON FUNCTION public.rp_smoke() TO rp_smoke_r WITH GRANT OPTION", "YES"},
			{"REVOKE GRANT OPTION FOR EXECUTE ON FUNCTION public.rp_smoke() FROM rp_smoke_r", "NO"},
			{"REVOKE EXECUTE ON FUNCTION public.rp_smoke() FROM rp_smoke_r", ""},
		} {
			c.run(peer, fixture.Exec(phase.query, 0))
			if phase.option == "" {
				read()
			} else {
				read(origin("rp_smoke_r", phase.option))
			}
		}
		c.exec("RESET SESSION AUTHORIZATION")
	})
}
