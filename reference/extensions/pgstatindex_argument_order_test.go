package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgstatindexPermissionArgumentOrder(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ps_argument_reader"})
	pgstatindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{"CREATE ROLE ps_argument_reader", "GRANT USAGE ON SCHEMA ps_part TO ps_argument_reader"} {
			run(fixture.Exec(q, 0))
		}
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		// pg_backend_pid is not immutable: the division must remain an executor
		// child, rather than a constant-folding failure before ACL initialization.
		for _, typ := range []string{"text", "regclass"} {
			q := "SELECT * FROM ps_part.pgstatindex((1/(pg_backend_pid()*0))::" + typ + ")"
			st, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer st.Close()
				held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
				check := func(code, message string) {
					s := pgstatindexError(q, code, message)
					pgstatindexBoth(t, p, b, c, s)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
				run(fixture.Exec("SET SESSION AUTHORIZATION ps_argument_reader", 0))
				check("42501", "permission denied for function pgstatindex")
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION ps_part.pgstatindex("+typ+") TO ps_argument_reader", 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION ps_argument_reader", 0))
				check("22012", "division by zero")
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION ps_part.pgstatindex("+typ+") FROM ps_argument_reader", 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION ps_argument_reader", 0))
				check("42501", "permission denied for function pgstatindex")
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			}()
		}
	})
}
