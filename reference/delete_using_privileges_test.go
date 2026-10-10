package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceDeleteUsingPrivilegeOracle(t *testing.T) {
	roleReferenceCleanup(t, []string{"delete_using_reader"})
	deleteUsingReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := updateFromRun(t, p, b, c)
		run(referenceExec("CREATE TABLE t(id INT,v INT)", 0))
		run(referenceExec("CREATE TABLE s(k INT,label TEXT)", 0))
		run(referenceExec("CREATE TABLE r(k INT)", 0))
		run(referenceExec("INSERT INTO t VALUES(1,10),(2,20)", 2))
		run(referenceExec("INSERT INTO s VALUES(1,'one')", 1))
		run(referenceExec("INSERT INTO r VALUES(1)", 1))
		if !b.pg {
			// Native public SQL has no ACL mutation API. The package-function
			// companion TestDeleteUsingTargetSourceColumnAuthority exercises
			// stable physical manifests and same-description revocation.
			run(referenceExec("DELETE FROM t d USING s WHERE d.id=s.k", 1))
			run(deleteUsingTargets([][]any{{int64(2), int64(20)}}))
			return
		}
		run(referenceExec("CREATE USER delete_using_reader", 0))
		run(referenceExec("GRANT USAGE ON SCHEMA public TO delete_using_reader", 0))
		run(referenceExec("GRANT DELETE ON t TO delete_using_reader", 0))
		run(referenceExec("SET SESSION AUTHORIZATION delete_using_reader", 0))
		defer c.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
		for _, q := range []string{"DELETE FROM t USING s WHERE FALSE", "DELETE FROM t USING s JOIN r ON FALSE WHERE FALSE", "DELETE FROM t USING s WHERE FALSE RETURNING 1"} {
			run(referenceErrorDiagnostic(q, controlsReferenceDiagnostic("42501", "permission denied for table s")))
		}
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("GRANT SELECT(k) ON s TO delete_using_reader", 0))
		run(referenceExec("GRANT SELECT(k) ON r TO delete_using_reader", 0))
		run(referenceExec("SET SESSION AUTHORIZATION delete_using_reader", 0))
		run(referenceExec("DELETE FROM t USING s WHERE FALSE", 0))
		for _, q := range []string{"DELETE FROM t USING s WHERE t.id=s.k", "DELETE FROM t USING s WHERE FALSE RETURNING old.id", "DELETE FROM t USING s WHERE FALSE RETURNING new.id"} {
			run(referenceErrorDiagnostic(q, controlsReferenceDiagnostic("42501", "permission denied for table t")))
		}
		run(referenceExec("DELETE FROM t USING s RETURNING 1", 2))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("INSERT INTO t VALUES(1,10),(2,20)", 2))
		run(referenceExec("GRANT SELECT(id) ON t TO delete_using_reader", 0))
		run(referenceExec("SET SESSION AUTHORIZATION delete_using_reader", 0))
		for _, q := range []string{"DELETE FROM t USING s WHERE t.id=s.k RETURNING s.label", "DELETE FROM t USING s JOIN r ON s.label='one' WHERE FALSE"} {
			run(referenceErrorDiagnostic(q, controlsReferenceDiagnostic("42501", "permission denied for table s")))
		}
		q := "DELETE FROM t USING s JOIN r USING(k) WHERE t.id=s.k RETURNING old.id,new.id,s.k"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
		referenceRunStep(t, p.ctx, held, b.recorder, referenceSelect(q, []string{"id", "id", "k"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), nil, int64(1)}}))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("INSERT INTO t VALUES(1,10)", 1))
		run(referenceExec("REVOKE SELECT(k) ON s FROM delete_using_reader", 0))
		run(referenceExec("SET SESSION AUTHORIZATION delete_using_reader", 0))
		referenceRunStep(t, p.ctx, held, b.recorder, referenceErrorDiagnostic(q, controlsReferenceDiagnostic("42501", "permission denied for table s")))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("GRANT SELECT(k) ON s TO delete_using_reader", 0))
		run(referenceExec("REVOKE DELETE ON t FROM delete_using_reader", 0))
		run(referenceExec("SET SESSION AUTHORIZATION delete_using_reader", 0))
		referenceRunStep(t, p.ctx, held, b.recorder, referenceErrorDiagnostic(q, controlsReferenceDiagnostic("42501", "permission denied for table t")))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(deleteUsingTargets([][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
		run(referenceSelect("SELECT k,label FROM s", []string{"k", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}}))
	})
}
