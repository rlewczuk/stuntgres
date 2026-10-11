package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func referenceLimitACL(t *testing.T, p *fixture.Pair) {
	for _, backend := range p.Backends() {
		run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step) }
		run(fixture.Exec("CREATE ROLE limit_reader", 0))
		run(fixture.Exec("CREATE TABLE acl_t(id INT,secret INT)", 0))
		run(fixture.Exec("INSERT INTO acl_t VALUES(1,9),(2,8)", 2))
		if backend.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO limit_reader", 0))
		}
		run(fixture.Exec("GRANT SELECT(id) ON acl_t TO limit_reader", 0))
		conn, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		defer conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("SET SESSION AUTHORIZATION limit_reader", 0))
		for _, query := range []string{
			"SELECT secret FROM acl_t LIMIT 0",
			"SELECT id FROM acl_t ORDER BY secret LIMIT 0",
			"SELECT secret FROM acl_t OFFSET 99",
			"SELECT secret FROM acl_t WHERE FALSE LIMIT 1",
		} {
			step := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for table acl_t"})
			step.SelectRows = true
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, step)
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Prepared(step))
		}
		step := fixture.Select("SELECT id FROM acl_t LIMIT 0", []string{"id"}, []string{"INT4"}, referenceLimitIDs())
		fixture.RunStep(t, p.Ctx, conn, backend.Recorder, step)
	}
}
