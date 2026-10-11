package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceGroupingSetsPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"gs_reader"})
	fixture.GroupingSetsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE gs_acl(a INT,hidden INT)", 0))
		run(fixture.Exec("INSERT INTO gs_acl VALUES(1,2)", 1))
		q := "SELECT GROUPING(hidden) AS g FROM gs_acl WHERE FALSE GROUP BY ROLLUP(hidden)"
		allowed := fixture.Select(q, []string{"g"}, []string{"INT4"}, [][]any{{int64(1)}})
		run(fixture.Exec("CREATE ROLE gs_reader", 0))
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO gs_reader", 0))
		}
		run(fixture.Exec("GRANT SELECT(a) ON gs_acl TO gs_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION gs_reader", 0))
		defer func() {
			if _, err := c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Error(err)
			}
		}()
		failure := fixture.GroupingSetsError(q, "42501", "permission denied for table gs_acl", "")
		// PostgreSQL permission checking is at execution, including prepared SQL.
		failure.ErrorStage = "execute"
		fixture.GroupingSetsBoth(run, failure)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT(hidden) ON gs_acl TO gs_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION gs_reader", 0))
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, allowed)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE SELECT(hidden) ON gs_acl FROM gs_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION gs_reader", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, failure)
	})
}
