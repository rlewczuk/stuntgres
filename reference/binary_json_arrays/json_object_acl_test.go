package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONObjectACL(t *testing.T) {
	const role = "jo_reader"
	fixture.RoleReferenceCleanup(t, []string{role})
	jsonObjectScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE USER "+role, 0))
		run(fixture.Exec("CREATE TABLE jo_acl(a text[],k text[],v text[])", 0))
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO "+role, 0))
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		deny := func(q string) {
			s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table jo_acl"))
			s.SelectRows = true
			s.ErrorStage = "execute"
			jsonObjectBoth(t, p, b, c, s)
		}
		for _, q := range []string{"SELECT json_object(a) FROM jo_acl", "SELECT json_object(k,v) FROM jo_acl WHERE FALSE"} {
			deny(q)
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("INSERT INTO jo_acl VALUES('{a,1}','{a}','{1}')", 1))
		run(fixture.Exec("GRANT SELECT(a,k) ON jo_acl TO "+role, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		jsonObjectBoth(t, p, b, c, jsonObjectSelect("SELECT json_object(a) AS v FROM jo_acl", `{"a" : "1"}`))
		deny("SELECT json_object(k,v) FROM jo_acl")
		deny("SELECT json_object(k,v) FROM jo_acl WHERE FALSE")
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT(v) ON jo_acl TO "+role, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		jsonObjectBoth(t, p, b, c, jsonObjectSelect("SELECT json_object(k,v) AS v FROM jo_acl", `{"a" : "1"}`))
	})
}
