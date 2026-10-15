package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONStripNullsACL(t *testing.T) {
	const role = "js_strip_reader"
	fixture.RoleReferenceCleanup(t, []string{role})
	fixture.SchemaReferenceCleanup(t, "js_strip_hidden")
	jsonStripScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { jsonTypeRun(t, p, b, c, s) }
		run(fixture.Exec("CREATE ROLE "+role, 0))
		run(fixture.Exec("CREATE TABLE js_acl(v json,flag bool)", 0))
		run(fixture.Exec(`INSERT INTO js_acl VALUES('{"a":null}',true),(NULL,false)`, 2))
		run(fixture.Exec("CREATE SCHEMA js_strip_hidden", 0))
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
			jsonTypeBoth(t, p, b, c, jsonTypeError("SELECT json_strip_nulls(v,flag) FROM js_acl"+tail, "42501", "permission denied for table js_acl", 0))
		}
		jsonTypeAnalysisError(t, p, b, c, jsonTypeError("SELECT js_strip_hidden.json_strip_nulls('{}')", "42501", "permission denied for schema js_strip_hidden", 8))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT(v) ON js_acl TO "+role, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		jsonTypeBoth(t, p, b, c, fixture.Select("SELECT json_strip_nulls(v,true) AS stripped FROM js_acl ORDER BY v::text NULLS LAST", []string{"stripped"}, []string{"JSON"}, [][]any{{"{}"}, {nil}}))
		for _, tail := range []string{"", " WHERE false", " WHERE v IS NULL"} {
			jsonTypeBoth(t, p, b, c, jsonTypeError("SELECT json_strip_nulls(v,flag) FROM js_acl"+tail, "42501", "permission denied for table js_acl", 0))
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT(flag) ON js_acl TO "+role, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		jsonTypeBoth(t, p, b, c, fixture.Select("SELECT json_strip_nulls(v,flag) AS stripped FROM js_acl ORDER BY v::text NULLS LAST", []string{"stripped"}, []string{"JSON"}, [][]any{{"{}"}, {nil}}))
	})
}
