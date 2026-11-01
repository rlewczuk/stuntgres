package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

var uvRoles = []string{"uv_o", "uv_r", "uv_d", "uv_m", "uv_g", "uv_x", "uv_y", "uv_p", "uv_power"}

const uvQuery = "SELECT * FROM information_schema.udt_privileges WHERE udt_schema=$1 AND udt_name=$2"
const uvRoleQuery = "SELECT * FROM information_schema.role_udt_grants WHERE udt_schema=$1 AND udt_name=$2"

type uvCase struct{ *ugCase }

func uvScenario(t *testing.T, body func(*uvCase)) {
	t.Helper()
	ugScenarioWith(t, uvRoles, []string{"uv_private", "uv_moved"}, func(c *ugCase) {
		body(&uvCase{c})
	})
}
func (c *uvCase) seed() {
	for _, role := range uvRoles {
		suffix := ""
		if role == "uv_power" {
			suffix = " SUPERUSER"
		}
		c.exec("CREATE ROLE " + role + suffix)
	}
	c.exec("GRANT USAGE ON SCHEMA public TO PUBLIC", "CREATE TYPE public.uv_t AS (a integer)", "ALTER TYPE public.uv_t OWNER TO uv_o")
}
func (c *uvCase) origin(grantor, grantee, option string) []any {
	return c.row(grantor, grantee, "public", "uv_t", option)
}
func (c *uvCase) public() []any { return c.origin("uv_o", "PUBLIC", "NO") }
func (c *uvCase) check(e fixture.Executor, schema, name string, want ...[]any) {
	c.t.Helper()
	c.checkQuery(e, uvQuery, schema, name, want...)
}
func (c *uvCase) checkQuery(e fixture.Executor, query, schema, name string, want ...[]any) {
	c.t.Helper()
	s := c.step(query, want, schema, name)
	c.run(e, s)
	if _, held := e.(fixture.HeldStatement); !held {
		c.run(e, fixture.Prepared(s))
	}
}
func (c *uvCase) acl(kind string) {
	c.t.Helper()
	q := "SELECT typacl IS NULL AS absent,COALESCE(cardinality(typacl),0)::bigint AS entries FROM pg_type WHERE oid='public.uv_t'::regtype"
	if kind == "nonempty" {
		q = "SELECT typacl IS NULL AS absent,cardinality(typacl)>0 AS nonempty FROM pg_type WHERE oid='public.uv_t'::regtype"
		c.run(c.conn, fixture.Select(q, []string{"absent", "nonempty"}, []string{"BOOL", "BOOL"}, [][]any{{false, true}}))
		return
	}
	c.run(c.conn, fixture.Select(q, []string{"absent", "entries"}, []string{"BOOL", "INT8"}, [][]any{{kind == "absent", int64(0)}}))
}
