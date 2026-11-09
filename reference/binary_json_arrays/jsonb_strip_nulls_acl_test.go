package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBStripNullsAuthority(t *testing.T) {
	const role = "jbs_strip_reader"
	fixture.RoleReferenceCleanup(t, []string{role})
	fixture.SchemaReferenceCleanup(t, "jbs_strip_hidden")
	jsonbStripScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { jsonTypeRun(t, p, b, c, s) }
		run(fixture.Exec("CREATE ROLE "+role, 0))
		run(fixture.Exec("CREATE TABLE jbs_acl(v jsonb,flag bool)", 0))
		run(fixture.Exec(`INSERT INTO jbs_acl VALUES('{"a":null}',true),(NULL,false)`, 2))
		run(fixture.Exec("CREATE SCHEMA jbs_strip_hidden", 0))
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO "+role, 0))
		} else {
			run(fixture.Exec("ALTER SCHEMA public OWNER TO "+role, 0))
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		defer func() {
			if _, err := c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Error(err)
				return
			}
			if !b.PG {
				if _, err := c.ExecContext(p.Ctx, "ALTER SCHEMA public OWNER TO CURRENT_USER"); err != nil {
					t.Error(err)
				}
			}
		}()
		for _, tail := range []string{"", " WHERE false"} {
			jsonTypeBoth(t, p, b, c, jsonTypeError("SELECT jsonb_strip_nulls(v,flag) FROM jbs_acl"+tail, "42501", "permission denied for table jbs_acl", 0))
		}
		jsonTypeAnalysisError(t, p, b, c, jsonTypeError("SELECT jbs_strip_hidden.jsonb_strip_nulls('{}')", "42501", "permission denied for schema jbs_strip_hidden", 8))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT(v) ON jbs_acl TO "+role, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		jsonTypeBoth(t, p, b, c, fixture.Select("SELECT jsonb_strip_nulls(v,true) AS stripped FROM jbs_acl ORDER BY v::text NULLS LAST", []string{"stripped"}, []string{"JSONB"}, [][]any{{"{}"}, {nil}}))
		for _, tail := range []string{"", " WHERE false", " WHERE v IS NULL"} {
			jsonTypeBoth(t, p, b, c, jsonTypeError("SELECT jsonb_strip_nulls(v,flag) FROM jbs_acl"+tail, "42501", "permission denied for table jbs_acl", 0))
		}
		jsonTypeBoth(t, p, b, c, jsonTypeError("SELECT jsonb_strip_nulls(NULL,flag) FROM jbs_acl WHERE false", "42501", "permission denied for table jbs_acl", 0))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE SELECT(v) ON jbs_acl FROM "+role, 0))
		run(fixture.Exec("GRANT SELECT(flag) ON jbs_acl TO "+role, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		for _, expression := range []string{"jsonb_strip_nulls(v,flag)", "jsonb_strip_nulls(strip_in_arrays => flag,target => v)", "jsonb_strip_nulls(v,NULL)"} {
			for _, tail := range []string{"", " WHERE false"} {
				jsonTypeBoth(t, p, b, c, jsonTypeError("SELECT "+expression+" FROM jbs_acl"+tail, "42501", "permission denied for table jbs_acl", 0))
			}
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("DELETE FROM jbs_acl", 2))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		jsonTypeBoth(t, p, b, c, jsonTypeError("SELECT jsonb_strip_nulls(v,flag) FROM jbs_acl", "42501", "permission denied for table jbs_acl", 0))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec(`INSERT INTO jbs_acl VALUES('{"a":null}',true),(NULL,false)`, 2))
		run(fixture.Exec("GRANT SELECT(v) ON jbs_acl TO "+role, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		jsonTypeBoth(t, p, b, c, fixture.Select("SELECT jsonb_strip_nulls(v,flag) AS stripped FROM jbs_acl ORDER BY v::text NULLS LAST", []string{"stripped"}, []string{"JSONB"}, [][]any{{"{}"}, {nil}}))
	})
}
