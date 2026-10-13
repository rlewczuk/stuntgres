package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func queryWindowExcludeAuthorization(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE ROLE ex_reader", 0),
		fixture.Exec("CREATE TABLE ex_secret(id INT,v INT)", 0),
		fixture.Exec("INSERT INTO ex_secret VALUES(1,10)", 1),
		fixture.Exec("GRANT SELECT(id) ON ex_secret TO ex_reader", 0),
	} {
		queryWindowRun(t, p, b, s)
	}
	if b.PG {
		queryWindowRun(t, p, b, fixture.Exec("GRANT USAGE ON SCHEMA public TO ex_reader", 0))
	}
	c, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET SESSION AUTHORIZATION ex_reader", 0))
	q := "SELECT sum(v) OVER(ROWS CURRENT ROW EXCLUDE GROUP) AS s FROM public.ex_secret"
	for _, sql := range []string{q, q + " WHERE FALSE", q + " LIMIT 0", "SELECT sum((SELECT v FROM public.ex_secret)) OVER(ROWS CURRENT ROW EXCLUDE GROUP) AS s"} {
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError(sql, fixture.Diagnostic{Code: "42501", Message: "permission denied for table ex_secret"})
			s.SelectRows = true
			s.Prepared = prepared
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
	}
	queryWindowRun(t, p, b, fixture.Exec("GRANT SELECT(v) ON ex_secret TO ex_reader", 0))
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{c, stmt, q}
	s := fixture.Select(q, []string{"s"}, []string{"INT8"}, [][]any{{nil}})
	fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	queryWindowRun(t, p, b, fixture.Exec("REVOKE SELECT(v) ON ex_secret FROM ex_reader", 0))
	failure := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table ex_secret"})
	failure.SelectRows = true
	failure.ErrorStage = "execute"
	fixture.RunStep(t, p.Ctx, held, b.Recorder, failure)
	queryWindowRun(t, p, b, fixture.Exec("GRANT SELECT(v) ON ex_secret TO ex_reader", 0))
	fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
}

func TestReferenceQueryWindowExcludeAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ex_reader"})
	queryWindowExcludeScenario(t, func(p *fixture.Pair, b fixture.Backend) { queryWindowExcludeAuthorization(t, p, b) })
}
