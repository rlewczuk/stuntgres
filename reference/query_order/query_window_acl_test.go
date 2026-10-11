package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryWindowPostgresAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"window_reader"})
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
		run(fixture.Exec("CREATE ROLE window_reader", 0))
		run(fixture.Exec("CREATE TABLE t(id INT,p TEXT,k INT)", 0))
		if backend.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO window_reader", 0))
		}
		c, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		fixture.RunStep(t, p.Ctx, c, backend.Recorder, fixture.Exec("SET SESSION AUTHORIZATION window_reader", 0))
		denied := func(q string) {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table t"})
			s.SelectRows = true
			for _, prepared := range []bool{false, true} {
				s.Prepared = prepared
				s.ErrorStage = "execute" // ACL is checked on execution, not PG Prepare.
				fixture.RunStep(t, p.Ctx, c, backend.Recorder, s)
			}
		}
		for _, q := range []string{"SELECT row_number() OVER() FROM t", "SELECT count(*) OVER() FROM t WHERE FALSE"} {
			denied(q)
		}
		run(fixture.Exec("GRANT SELECT(id) ON t TO window_reader", 0))
		q := "SELECT row_number() OVER(),count(*) OVER() FROM t"
		s := fixture.Select(q, []string{"row_number", "count"}, []string{"INT8", "INT8"}, nil)
		fixture.RunStep(t, p.Ctx, c, backend.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, backend.Recorder, fixture.Prepared(s))
		for _, q := range []string{
			"SELECT row_number() OVER(PARTITION BY p) FROM t",
			"SELECT count(*) OVER(ORDER BY k) FROM t WHERE FALSE",
			"SELECT id FROM t ORDER BY row_number() OVER(ORDER BY k)",
			"SELECT id FROM t WINDOW unused AS(PARTITION BY p)",
		} {
			denied(q)
		}
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{c, stmt, q}
		fixture.RunStep(t, p.Ctx, held, backend.Recorder, s)
		run(fixture.Exec("REVOKE SELECT(id) ON t FROM window_reader", 0))
		failure := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table t"})
		failure.SelectRows = true
		failure.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, backend.Recorder, failure)
	}
}
