package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectACLValues(t *testing.T) {
	objectACLScenario(t, []string{"acl_obj_reader"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { objectACLRun(t, p, b, c, s) }
		run(fixture.Exec("CREATE TABLE acl_obj_values(a INT,b INT)", 0))
		run(fixture.Exec("CREATE TABLE acl_obj_unrelated(a INT)", 0))
		check := func(whole, a, second any) {
			t.Helper()
			run(fixture.Select("SELECT pg_get_acl(1259,'acl_obj_values'::regclass,0) AS whole,pg_get_acl(1259,'acl_obj_values'::regclass,1) AS a,pg_get_acl(1259,'acl_obj_values'::regclass,2) AS b,pg_get_acl(1259,'acl_obj_unrelated'::regclass,0) AS unrelated", []string{"whole", "a", "b", "unrelated"}, []string{"_ACLITEM", "_ACLITEM", "_ACLITEM", "_ACLITEM"}, [][]any{{whole, a, second, nil}}))
		}
		expand := func(subid string, entries ...string) {
			t.Helper()
			rows := make([][]any, len(entries))
			for i, entry := range entries {
				rows[i] = []any{entry}
			}
			s := fixture.Select("SELECT unnest(pg_get_acl(1259,'acl_obj_values'::regclass,"+subid+")) AS acl", []string{"acl"}, []string{"ACLITEM"}, rows)
			s.Bag = true
			run(s)
		}
		check(nil, nil, nil)
		expand("1")
		run(fixture.Exec("GRANT SELECT(a) ON acl_obj_values TO PUBLIC", 0))
		check(nil, "{=r/"+objectACLOwner(b)+"}", nil)
		expand("1", "=r/"+objectACLOwner(b))
		run(fixture.Exec("GRANT UPDATE(a) ON acl_obj_values TO acl_obj_reader WITH GRANT OPTION", 0))
		// Multiple ACL entries are an exact bag, not an SRF ordering contract.
		expand("1", "=r/"+objectACLOwner(b), "acl_obj_reader=w*/"+objectACLOwner(b))
		run(fixture.Select("SELECT pg_get_acl(1259,'acl_obj_values'::regclass,0) IS NULL AS whole,pg_get_acl(1259,'acl_obj_values'::regclass,2) IS NULL AS b,cardinality(pg_get_acl(1259,'acl_obj_values'::regclass,1)) AS n", []string{"whole", "b", "n"}, []string{"BOOL", "BOOL", "INT4"}, [][]any{{true, true, int64(2)}}))
		run(fixture.Exec("GRANT SELECT ON acl_obj_values TO acl_obj_reader", 0))
		expand("0", objectACLOwner(b)+"=arwdDxtm/"+objectACLOwner(b), "acl_obj_reader=r/"+objectACLOwner(b))
		expand("1", "=r/"+objectACLOwner(b), "acl_obj_reader=w*/"+objectACLOwner(b))
		run(fixture.Exec("REVOKE GRANT OPTION FOR UPDATE(a) ON acl_obj_values FROM acl_obj_reader", 0))
		expand("1", "=r/"+objectACLOwner(b), "acl_obj_reader=w/"+objectACLOwner(b))
		run(fixture.Exec("REVOKE UPDATE(a) ON acl_obj_values FROM acl_obj_reader", 0))
		expand("1", "=r/"+objectACLOwner(b))
		run(fixture.Exec("REVOKE SELECT(a) ON acl_obj_values FROM PUBLIC", 0))
		expand("1")
		run(objectACLSelect("SELECT pg_get_acl(1259,'acl_obj_values'::regclass,1) AS acl", nil))
		expand("0", objectACLOwner(b)+"=arwdDxtm/"+objectACLOwner(b), "acl_obj_reader=r/"+objectACLOwner(b))
		run(objectACLSelect("SELECT pg_get_acl(1259,'acl_obj_unrelated'::regclass,0) AS acl", nil))
		run(fixture.Exec("CREATE TABLE acl_obj_empty(a INT)", 0))
		run(fixture.Exec("REVOKE ALL ON acl_obj_empty FROM "+objectACLOwner(b), 0))
		run(objectACLSelect("SELECT pg_get_acl(1259,'acl_obj_empty'::regclass,0) AS acl", "{}"))
		run(fixture.Select("SELECT pg_get_acl(1259,'acl_obj_empty'::regclass,0) IS NULL AS absent,cardinality(pg_get_acl(1259,'acl_obj_empty'::regclass,0)) AS n", []string{"absent", "n"}, []string{"BOOL", "INT4"}, [][]any{{false, int64(0)}}))
		run(fixture.Select("SELECT unnest(pg_get_acl(1259,'acl_obj_empty'::regclass,0)) AS acl", []string{"acl"}, []string{"ACLITEM"}, nil))
	})
}

func TestReferenceObjectACLTypes(t *testing.T) {
	objectACLScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { objectACLRun(t, p, b, c, s) }
		for _, q := range []string{
			"CREATE DOMAIN acl_obj_domain AS INT",
			"CREATE TYPE acl_obj_enum AS ENUM ('one','two')",
			"CREATE TYPE acl_obj_composite AS (a INT)",
			"CREATE TABLE acl_obj_rowtype(a INT)",
		} {
			run(fixture.Exec(q, 0))
		}
		for _, name := range []string{"acl_obj_domain", "acl_obj_domain[]", "acl_obj_enum", "acl_obj_enum[]", "acl_obj_composite", "acl_obj_composite[]", "acl_obj_rowtype", "acl_obj_rowtype[]", "integer", "integer[]"} {
			run(objectACLSelect("SELECT pg_get_acl(1247,'"+name+"'::regtype,0) AS acl", nil))
		}
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES REVOKE USAGE ON TYPES FROM PUBLIC", 0))
		// Register the inverse immediately: defaults are not removed by public reset.
		defer func() {
			if _, err := c.ExecContext(p.Ctx, "ALTER DEFAULT PRIVILEGES GRANT USAGE ON TYPES TO PUBLIC"); err != nil {
				t.Error(err)
			}
		}()
		run(fixture.Exec("CREATE DOMAIN acl_obj_private_domain AS INT", 0))
		run(fixture.Exec("CREATE TYPE acl_obj_private_enum AS ENUM ('one')", 0))
		run(fixture.Exec("CREATE TYPE acl_obj_private_composite AS (a INT)", 0))
		for _, name := range []string{"acl_obj_private_domain", "acl_obj_private_enum", "acl_obj_private_composite"} {
			run(objectACLSelect("SELECT pg_get_acl(1247,'"+name+"'::regtype,0) AS acl", "{"+objectACLOwner(b)+"=U/"+objectACLOwner(b)+"}"))
		}
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES GRANT USAGE ON TYPES TO PUBLIC", 0))
		run(fixture.Exec("CREATE DOMAIN acl_obj_baseline_domain AS INT", 0))
		run(objectACLSelect("SELECT pg_get_acl(1247,'acl_obj_baseline_domain'::regtype,0) AS acl", nil))
		// Existing types retain the raw ACL chosen at their creation.
		run(objectACLSelect("SELECT pg_get_acl(1247,'acl_obj_private_domain'::regtype,0) AS acl", "{"+objectACLOwner(b)+"=U/"+objectACLOwner(b)+"}"))
	})
}
