package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceGroupingSetsPrivileges(t *testing.T) {
	roleReferenceCleanup(t, []string{"gs_reader"})
	groupingSetsScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep)) {
		run(referenceExec("CREATE TABLE gs_acl(a INT,hidden INT)", 0))
		run(referenceExec("INSERT INTO gs_acl VALUES(1,2)", 1))
		q := "SELECT GROUPING(hidden) AS g FROM gs_acl WHERE FALSE GROUP BY ROLLUP(hidden)"
		allowed := referenceSelect(q, []string{"g"}, []string{"INT4"}, [][]any{{int64(1)}})
		run(referenceExec("CREATE ROLE gs_reader", 0))
		if b.pg { run(referenceExec("GRANT USAGE ON SCHEMA public TO gs_reader", 0)) }
		run(referenceExec("GRANT SELECT(a) ON gs_acl TO gs_reader", 0))
		run(referenceExec("SET SESSION AUTHORIZATION gs_reader", 0))
		defer func() {
			if _, err := c.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Error(err)
			}
		}()
		failure := groupingSetsError(q, "42501", "permission denied for table gs_acl", "")
		// PostgreSQL permission checking is at execution, including prepared SQL.
		failure.errorStage = "execute"
		groupingSetsBoth(run, failure)
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("GRANT SELECT(hidden) ON gs_acl TO gs_reader", 0))
		run(referenceExec("SET SESSION AUTHORIZATION gs_reader", 0))
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
		referenceRunStep(t, p.ctx, held, b.recorder, allowed)
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("REVOKE SELECT(hidden) ON gs_acl FROM gs_reader", 0))
		run(referenceExec("SET SESSION AUTHORIZATION gs_reader", 0))
		referenceRunStep(t, p.ctx, held, b.recorder, failure)
	})
}
