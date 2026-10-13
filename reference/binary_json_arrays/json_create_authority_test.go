package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonCreateAuthority(t *testing.T) {
	const role = "jc_reader"
	fixture.RoleReferenceCleanup(t, []string{role})
	jsonCreateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE USER "+role, 0))
		run(fixture.Exec(`CREATE TABLE jc_acl(n int,t text)`, 0))
		run(fixture.Exec(`INSERT INTO jc_acl VALUES(1,'x')`, 1))
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO "+role, 0))
		}
		run(fixture.Exec("GRANT SELECT(n) ON jc_acl TO "+role, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		jsonObjectBoth(t, p, b, c, jsonCreateSelect(`SELECT to_json(n) AS v FROM jc_acl`, "JSON", `1`))
		for _, q := range []string{
			`SELECT to_json(t) FROM jc_acl WHERE FALSE`,
			`SELECT to_jsonb(jc_acl) FROM jc_acl`,
			`SELECT row_to_json(jc_acl) FROM jc_acl WHERE FALSE`,
			`SELECT json_build_array(n,t) FROM jc_acl`,
			`SELECT jsonb_build_object(n,t) FROM jc_acl`,
		} {
			t.Run(q, func(t *testing.T) {
				jsonCreateError(t, p, b, c, q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table jc_acl"), false)
			})
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT(t) ON jc_acl TO "+role, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		jsonObjectBoth(t, p, b, c, jsonCreateSelect(`SELECT row_to_json(jc_acl) AS v FROM jc_acl`, "JSON", `{"n":1,"t":"x"}`))
		jsonObjectBoth(t, p, b, c, jsonCreateSelect(`SELECT jsonb_build_array(n,t) AS v FROM jc_acl`, "JSONB", `[1, "x"]`))
	})
}
