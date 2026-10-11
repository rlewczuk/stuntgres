package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryAliasesPostgresColumnACL(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"alias_reader"})
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step) }
		run(fixture.Exec("CREATE ROLE alias_reader", 0))
		if backend.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO alias_reader", 0))
		}
		for _, step := range []fixture.Step{
			fixture.Exec("CREATE TABLE acl_t(id INT,secret INT)", 0),
			fixture.Exec("CREATE TABLE acl_empty(id INT,secret INT)", 0),
			fixture.Exec("INSERT INTO acl_t VALUES(1,9),(2,8)", 2),
			fixture.Exec("GRANT SELECT(id) ON acl_t,acl_empty TO alias_reader", 0),
		} {
			run(step)
		}
		conn, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		defer conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("SET SESSION AUTHORIZATION alias_reader", 0))
		q := "SELECT x.iid AS secret FROM acl_t x(iid) ORDER BY secret"
		stmt, err := conn.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal("authorized prepare failed", err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
		step := fixture.Select(q, []string{"secret"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
		fixture.RunStep(t, p.Ctx, conn, backend.Recorder, step)
		fixture.RunStep(t, p.Ctx, held, backend.Recorder, step)
		for _, table := range []string{"acl_t", "acl_empty"} {
			for _, query := range []string{
				"SELECT x.secret FROM " + table + " x(iid)",
				"SELECT x.iid AS secret FROM " + table + " x(iid) WHERE secret=9",
			} {
				step := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for table " + table})
				step.SelectRows, step.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, step)
				// PostgreSQL analysis permits this Prepare; permission is checked
				// at execution, independently of empty/populated source rows.
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Prepared(step))
			}
		}
		if _, err := backend.DB.ExecContext(p.Ctx, "REVOKE SELECT(id) ON acl_t,acl_empty FROM alias_reader"); err != nil {
			t.Fatal(err)
		}
		denied := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table acl_t"})
		denied.SelectRows, denied.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, held, backend.Recorder, denied)
		for _, table := range []string{"acl_t", "acl_empty"} {
			step := fixture.ImplicitError("SELECT 1 AS secret FROM "+table+" x(iid) WHERE FALSE", fixture.Diagnostic{Code: "42501", Message: "permission denied for table " + table})
			step.SelectRows, step.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, step)
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Prepared(step))
		}
	}
}
