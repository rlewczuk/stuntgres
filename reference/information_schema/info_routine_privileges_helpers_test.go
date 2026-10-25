package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

const rpQuery = "SELECT * FROM information_schema.routine_privileges WHERE routine_schema=$1 AND routine_name=$2"

var rpRoles = []string{"rp_o", "rp_r", "rp_d", "rp_g", "rp_m", "rp_x", "rp_y", "rp_power", "rp_smoke_o", "rp_smoke_r", "rp_smoke_x"}

func rpScenario(t *testing.T, body func(*rrgCase)) {
	t.Helper()
	routineACLScenario(t, rpRoles, []string{"rp_private", "rp_moved", "rp_ext"}, func(c *rrgCase) {
		c.query = rpQuery
		body(c)
	})
}

func rpSeed(c *rrgCase) {
	c.t.Helper()
	c.exec("CREATE ROLE rp_o", "CREATE ROLE rp_r", "CREATE ROLE rp_d", "CREATE ROLE rp_g", "CREATE ROLE rp_m", "CREATE ROLE rp_x", "CREATE ROLE rp_y", "CREATE ROLE rp_power SUPERUSER", "GRANT USAGE ON SCHEMA public TO PUBLIC", "CREATE FUNCTION public.rp_f() RETURNS integer RETURN 7", "ALTER FUNCTION public.rp_f() OWNER TO rp_o")
}

func rpOrigin(c *rrgCase, grantor, grantee, option string) []any {
	c.t.Helper()
	schema, name, _, specific, _ := c.identity("public.rp_f()")
	return c.row(grantor, grantee, schema, name, specific, option)
}

func rpRead(c *rrgCase, held fixture.HeldStatement, want ...[]any) {
	c.t.Helper()
	c.check(c.conn, "public", "rp_f", want...)
	c.check(held, "public", "rp_f", want...)
}

func rpRoleView(c *rrgCase, want ...[]any) {
	c.t.Helper()
	s := c.step(rrgQuery, want, "public", "rp_f")
	c.run(c.conn, s)
	c.run(c.conn, fixture.Prepared(s))
}
