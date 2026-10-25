package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantACLGetterProviderAuthority(t *testing.T) {
	objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
		run(fixture.Exec("CREATE TABLE og_acl_subject(a INT)", 0))
		run(fixture.Exec("GRANT SELECT(a) ON og_acl_subject TO PUBLIC", 0))
		run(fixture.Exec("CREATE TABLE og_acl_provider(id INT,s INT)", 0))
		run(fixture.Exec("INSERT INTO og_acl_provider VALUES(1,1),(2,0),(3,NULL)", 3))
		run(fixture.Exec("GRANT SELECT ON og_acl_provider TO og_object_reader", 0))
		// Independent fixture identities, not a name obtained from an ACL result.
		owner := "postgres"
		if b.PG {
			owner = "stuntgres"
		}
		want := "{=r/" + owner + "}"
		call := "pg_get_acl(1259,'og_acl_subject'::regclass,s)"
		run(fixture.Exec("CREATE VIEW og_acl_view AS SELECT "+call+" AS v FROM og_acl_provider WHERE id=1", 0))
		run(fixture.Exec("GRANT SELECT ON og_acl_view TO og_object_reader", 0))
		selectValue := func(expr, tail string, value any, args ...any) fixture.Step {
			return fixture.Select("SELECT "+expr+" AS v FROM og_acl_provider"+tail, []string{"v"}, []string{"_ACLITEM"}, [][]any{{value}}, args...)
		}
		cases := []objectGrantProviderCase{
			{selectValue(call, " WHERE id=1", want), true, true},
			{selectValue(call, " WHERE id=2", nil), true, true},
			{selectValue(call, " WHERE id=3", nil), true, true},
			{fixture.Select("SELECT v FROM og_acl_view", []string{"v"}, []string{"_ACLITEM"}, [][]any{{want}}), true, true},
			{selectValue("pg_get_acl(objsubid=>s,objid=>'og_acl_subject'::regclass,classid=>1259)", " WHERE id=1", want), true, true},
			{selectValue("pg_get_acl(1259,'og_acl_subject'::regclass,$1::INTEGER)", " WHERE id=1", want, int64(1)), true, true},
			{selectValue("pg_get_acl(1259,'og_acl_subject'::regclass,$1::INTEGER)", " WHERE id=1", nil, nil), false, false},
			{selectValue("pg_get_acl(NULL::OID,'og_acl_subject'::regclass,s)", " WHERE id=1", nil), false, true},
			{selectValue("pg_get_acl(1259,NULL::OID,s)", " WHERE id=1", nil), false, true},
			{selectValue("pg_get_acl(1259,'og_acl_subject'::regclass,NULL::INTEGER)", " WHERE id=1", nil), false, true},
			{selectValue("pg_get_acl(1259,0,s)", " WHERE id=1", nil), true, true},
			{selectValue("CASE WHEN FALSE THEN "+call+" ELSE NULL END", " WHERE id=1", nil), false, true},
			{fixture.Select("SELECT "+call+" AS v FROM og_acl_provider WHERE id>99", []string{"v"}, []string{"_ACLITEM"}, nil), true, true},
			{fixture.Select("SELECT "+call+" AS v FROM og_acl_provider WHERE FALSE", []string{"v"}, []string{"_ACLITEM"}, nil), true, true},
		}
		control := fixture.Select("SELECT pg_has_role('og_object_reader','MEMBER') AS v", []string{"v"}, []string{"BOOL"}, [][]any{{true}})
		objectGrantProviderLifecycle(t, p, b, c, run, prepared, "pg_catalog.pg_get_acl(oid,oid,integer)", "function", "pg_get_acl", cases, []fixture.Step{control})
	})
}
