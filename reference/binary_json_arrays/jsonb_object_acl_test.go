package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBObjectACL(t *testing.T) {
	const role = "jbo_reader"
	fixture.RoleReferenceCleanup(t, []string{role})
	jsonbObjectScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE USER "+role, 0))
		run(fixture.Exec("CREATE TABLE jbo_acl(a text[],k text[],v text[])", 0))
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO "+role, 0))
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		deny := func(q string) {
			s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table jbo_acl"))
			s.SelectRows = true
			s.ErrorStage = "execute"
			jsonObjectBoth(t, p, b, c, s)
		}
		for _, q := range []string{"SELECT jsonb_object(a) FROM jbo_acl", "SELECT jsonb_object(k,v) FROM jbo_acl WHERE FALSE"} {
			deny(q)
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		// Each sibling is authorized even on an empty relation or when strictness
		// makes its value unnecessary. Grant one column at a time, in both orders.
		run(fixture.Exec("GRANT SELECT(k) ON jbo_acl TO "+role, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		deny("SELECT jsonb_object(k,v) FROM jbo_acl")
		deny("SELECT jsonb_object(NULL::text[],v) FROM jbo_acl WHERE FALSE")
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE SELECT(k) ON jbo_acl FROM "+role, 0))
		run(fixture.Exec("GRANT SELECT(v) ON jbo_acl TO "+role, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		deny("SELECT jsonb_object(k,v) FROM jbo_acl")
		deny("SELECT jsonb_object(k,NULL::text[]) FROM jbo_acl WHERE FALSE")
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE SELECT(v) ON jbo_acl FROM "+role, 0))
		run(fixture.Exec("INSERT INTO jbo_acl VALUES('{a,1}','{a}','{1}')", 1))
		run(fixture.Exec("GRANT SELECT(a,k) ON jbo_acl TO "+role, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		jsonObjectBoth(t, p, b, c, jsonbObjectSelect("SELECT jsonb_object(a) AS v FROM jbo_acl", `{"a": "1"}`))
		deny("SELECT jsonb_object(k,v) FROM jbo_acl")
		deny("SELECT jsonb_object(k,v) FROM jbo_acl WHERE FALSE")
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT(v) ON jbo_acl TO "+role, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		jsonObjectBoth(t, p, b, c, jsonbObjectSelect("SELECT jsonb_object(k,v) AS v FROM jbo_acl", `{"a": "1"}`))
	})
}
