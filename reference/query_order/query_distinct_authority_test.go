package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryDistinctAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"distinct_reader"})
	fixture.QueryDistinctScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE acl_t(id INT,secret INT)", 0))
		run(fixture.Exec("CREATE TABLE acl_empty(id INT,secret INT)", 0))
		run(fixture.Exec("INSERT INTO acl_t VALUES(1,9),(1,8),(2,8)", 3))
		q := "SELECT DISTINCT id AS secret FROM acl_t ORDER BY secret"
		fixture.QueryDistinctBoth(run, fixture.Select(q, []string{"secret"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO PUBLIC", 0))
		}
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE ROLE distinct_reader", 0),
			fixture.Exec("GRANT SELECT(id) ON acl_t,acl_empty TO distinct_reader", 0),
			fixture.Exec("SET SESSION AUTHORIZATION distinct_reader", 0),
		} {
			run(s)
		}
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{c, stmt, q}
		s := fixture.Select(q, []string{"secret"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
		run(s)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		for _, table := range []string{"acl_t", "acl_empty"} {
			for _, query := range []string{
				"SELECT DISTINCT secret FROM " + table,
				"SELECT DISTINCT secret FROM " + table + " WHERE FALSE",
				"SELECT DISTINCT id AS secret FROM " + table + " WHERE secret=9",
			} {
				s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for table " + table})
				s.SelectRows = true
				s.ErrorStage = "execute"
				fixture.QueryDistinctBoth(run, s)
			}
		}
		if _, err := b.DB.ExecContext(p.Ctx, "REVOKE SELECT(id) ON acl_t,acl_empty FROM distinct_reader"); err != nil {
			t.Fatal(err)
		}
		s = fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table acl_t"})
		s.SelectRows = true
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		for _, table := range []string{"acl_t", "acl_empty"} {
			s := fixture.ImplicitError("SELECT DISTINCT 1 FROM "+table+" WHERE FALSE", fixture.Diagnostic{Code: "42501", Message: "permission denied for table " + table})
			s.SelectRows = true
			s.ErrorStage = "execute"
			fixture.QueryDistinctBoth(run, s)
		}
	})
}
