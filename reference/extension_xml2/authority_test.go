package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func xml2ACLSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, q := range []string{
		"CREATE ROLE xml2_probe_user", "CREATE ROLE xml2_probe_denied", "CREATE ROLE xml2_provider_owner",
		"GRANT USAGE ON SCHEMA public TO xml2_probe_user,xml2_probe_denied",
		"CREATE VIEW xml2_probe_view AS SELECT xpath_number('<r>12</r>','.') AS n",
		"GRANT SELECT ON xml2_probe_view TO xml2_probe_user,xml2_probe_denied",
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
	}
	q := "SELECT xpath_number($1::text,$2::text) AS n"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	h := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	want := fixture.Select(q, []string{"n"}, []string{"FLOAT4"}, [][]any{{float64(12)}}, "<r>12</r>", ".")
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET ROLE xml2_probe_user", 0))
	fixture.RunStep(t, p.Ctx, h, b.Recorder, want)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET ROLE", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("REVOKE EXECUTE ON FUNCTION xpath_number(text,text) FROM PUBLIC", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET ROLE xml2_probe_user", 0))
	denied := xml2Error("42501", "permission denied for function xpath_number")
	s := fixture.ErrorDiagnostic(q, denied, "<r>12</r>", ".")
	s.SelectRows, s.ErrorStage = true, "execute"
	fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
	xml2Failure(t, p, b, c, "SELECT xpath_number('<r>12</r>','.') AS n", denied, false)
	xml2Failure(t, p, b, c, "SELECT n FROM xml2_probe_view", denied, false)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET ROLE", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("GRANT EXECUTE ON FUNCTION xpath_number(text,text) TO xml2_probe_user", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET ROLE xml2_probe_user", 0))
	fixture.RunStep(t, p.Ctx, h, b.Recorder, want)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET ROLE", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER FUNCTION xpath_number(text,text) OWNER TO xml2_provider_owner", 0))
	xml2ACLCheck(t, p, b, c)
}

func xml2ACLCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	xml2Both(t, p, b, c, fixture.Select("SELECT r.rolname FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='public' AND p.proname='xpath_number'", []string{"rolname"}, []string{"NAME"}, [][]any{{"xml2_provider_owner"}}))
	for _, role := range []string{"xml2_probe_user", "xml2_probe_denied"} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET ROLE "+role, 0))
		for _, q := range []string{"SELECT xpath_number('<r>12</r>','.') AS n", "SELECT n FROM xml2_probe_view"} {
			if role == "xml2_probe_user" {
				xml2Both(t, p, b, c, fixture.Select(q, []string{"n"}, []string{"FLOAT4"}, [][]any{{float64(12)}}))
			} else {
				xml2Failure(t, p, b, c, q, xml2Error("42501", "permission denied for function xpath_number"), false)
			}
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET ROLE", 0))
	}
}

func xml2RecreatedPrincipal(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, q := range []string{
		"REVOKE EXECUTE ON FUNCTION xpath_number(text,text) FROM xml2_probe_user",
		"REVOKE SELECT ON xml2_probe_view FROM xml2_probe_user",
		"REVOKE USAGE ON SCHEMA public FROM xml2_probe_user",
		"DROP ROLE xml2_probe_user", "CREATE ROLE xml2_probe_user",
		"GRANT USAGE ON SCHEMA public TO xml2_probe_user", "SET ROLE xml2_probe_user",
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
	}
	xml2Failure(t, p, b, c, "SELECT xpath_number('<r>12</r>','.') AS n", xml2Error("42501", "permission denied for function xpath_number"), false)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET ROLE", 0))
}

func xml2SchemaAuthority(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("REVOKE USAGE ON SCHEMA public FROM PUBLIC,xml2_probe_user", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET ROLE xml2_probe_user", 0))
	d := xml2Error("42501", "permission denied for schema public")
	d.Position = "8"
	xml2Failure(t, p, b, c, "SELECT public.xpath_string('<r/>','.')", d, true)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET ROLE", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("GRANT USAGE ON SCHEMA public TO PUBLIC,xml2_probe_user", 0))
	xml2ACLCheck(t, p, b, c)
}
