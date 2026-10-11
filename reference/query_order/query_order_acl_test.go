package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryOrderPostgresColumnACL(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"order_reader"})
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step) }
		run(fixture.Exec("CREATE ROLE order_reader", 0))
		run(fixture.Exec("CREATE TABLE acl_t(id INT,secret INT)", 0))
		run(fixture.Exec("INSERT INTO acl_t VALUES(1,9),(2,8)", 2))
		if backend.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO order_reader", 0))
		}
		for _, q := range []string{"GRANT SELECT(id) ON acl_t TO order_reader"} {
			run(fixture.Exec(q, 0))
		}
		conn, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		defer conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("SET SESSION AUTHORIZATION order_reader", 0))
		for _, q := range []string{"SELECT id AS secret FROM acl_t ORDER BY secret", "SELECT id AS secret FROM acl_t ORDER BY 1"} {
			step := fixture.Select(q, []string{"secret"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, step)
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Prepared(step))
		}
		for _, q := range []string{"SELECT id FROM acl_t ORDER BY secret+1", "SELECT id AS secret FROM acl_t ORDER BY acl_t.secret"} {
			step := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table acl_t"})
			step.SelectRows = true
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, step)
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Prepared(step))
		}
	}
}
