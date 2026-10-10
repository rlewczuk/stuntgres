package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceQueryDistinctAuthority(t *testing.T) {
	roleReferenceCleanup(t, []string{"distinct_reader"})
	queryDistinctScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE acl_t(id INT,secret INT)", 0))
		run(referenceExec("CREATE TABLE acl_empty(id INT,secret INT)", 0))
		run(referenceExec("INSERT INTO acl_t VALUES(1,9),(1,8),(2,8)", 3))
		q := "SELECT DISTINCT id AS secret FROM acl_t ORDER BY secret"
		queryDistinctBoth(run, referenceSelect(q, []string{"secret"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		if b.pg { run(referenceExec("GRANT USAGE ON SCHEMA public TO PUBLIC", 0)) }
		for _, s := range []referenceStep{
			referenceExec("CREATE ROLE distinct_reader", 0),
			referenceExec("GRANT SELECT(id) ON acl_t,acl_empty TO distinct_reader", 0),
			referenceExec("SET SESSION AUTHORIZATION distinct_reader", 0),
		} {
			run(s)
		}
		defer c.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{c, stmt, q}
		s := referenceSelect(q, []string{"secret"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
		run(s)
		referenceRunStep(t, p.ctx, held, b.recorder, s)
		for _, table := range []string{"acl_t", "acl_empty"} {
			for _, query := range []string{
				"SELECT DISTINCT secret FROM " + table,
				"SELECT DISTINCT secret FROM " + table + " WHERE FALSE",
				"SELECT DISTINCT id AS secret FROM " + table + " WHERE secret=9",
			} {
				s := referenceImplicitError(query, referenceDiagnostic{Code: "42501", Message: "permission denied for table " + table})
				s.selectRows = true
				s.errorStage = "execute"
				queryDistinctBoth(run, s)
			}
		}
		if _, err := b.db.ExecContext(p.ctx, "REVOKE SELECT(id) ON acl_t,acl_empty FROM distinct_reader"); err != nil {
			t.Fatal(err)
		}
		s = referenceImplicitError(q, referenceDiagnostic{Code: "42501", Message: "permission denied for table acl_t"})
		s.selectRows = true
		s.errorStage = "execute"
		referenceRunStep(t, p.ctx, held, b.recorder, s)
		for _, table := range []string{"acl_t", "acl_empty"} {
			s := referenceImplicitError("SELECT DISTINCT 1 FROM "+table+" WHERE FALSE", referenceDiagnostic{Code: "42501", Message: "permission denied for table " + table})
			s.selectRows = true
			s.errorStage = "execute"
			queryDistinctBoth(run, s)
		}
	})
}
