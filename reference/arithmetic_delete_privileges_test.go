//go:build reference

package reference_test

import "testing"

// Native authorization is exercised by TestExpressionArithmeticDeleteAuthorization;
// GRANT here is PostgreSQL fixture authority, not a native implementation.
func TestReferenceArithmeticDeletePrivileges(t *testing.T) {
	const role = "arithmetic_delete_actor"
	for _, query := range []string{"DELETE FROM t WHERE i+1=8", "DELETE FROM t WHERE CAST(i AS bigint)+1=8", "DELETE FROM t WHERE b+i=14"} {
		for _, prepared := range []bool{false, true} {
			t.Run(query+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				roleReferenceCleanup(t, []string{role})
				p := newReferencePair(t)
				admin, actor := referenceLockConnections(t, p.ctx, p.postgres)
				for _, sql := range []string{"CREATE USER " + role, "CREATE TABLE t(id INT,i INT,b BIGINT)", "GRANT USAGE ON SCHEMA public TO " + role, "GRANT DELETE ON TABLE t TO " + role} {
					referenceRunStep(t, p.ctx, admin, p.postgresRecord, referenceExec(sql, 0))
				}
				referenceRunStep(t, p.ctx, admin, p.postgresRecord, referenceExec("INSERT INTO t VALUES(1,7,7),(2,-7,-7),(3,NULL,NULL),(4,7,7)", 4))
				referenceRunStep(t, p.ctx, actor, p.postgresRecord, referenceExec("SET SESSION AUTHORIZATION "+role, 0))
				t.Cleanup(func() {
					referenceRunStep(t, p.ctx, actor, p.postgresRecord, referenceExec("RESET SESSION AUTHORIZATION", 0))
				})
				original := referenceSelect("SELECT id,i,b FROM t ORDER BY id", []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(7), int64(7)}, {int64(2), int64(-7), int64(-7)}, {int64(3), nil, nil}, {int64(4), int64(7), int64(7)}})
				denied := referenceErrorDiagnostic(query, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table t"})
				denied.prepared, denied.errorStage = prepared, "execute"
				referenceRunStep(t, p.ctx, actor, p.postgresRecord, denied)
				referenceRunStep(t, p.ctx, admin, p.postgresRecord, original)
				referenceRunStep(t, p.ctx, admin, p.postgresRecord, referenceExec("GRANT SELECT(i) ON TABLE t TO "+role, 0))
				if query == "DELETE FROM t WHERE b+i=14" {
					referenceRunStep(t, p.ctx, actor, p.postgresRecord, denied)
					referenceRunStep(t, p.ctx, admin, p.postgresRecord, original)
					referenceRunStep(t, p.ctx, admin, p.postgresRecord, referenceExec("GRANT SELECT(b) ON TABLE t TO "+role, 0))
				}
				success := referenceExec(query, 2)
				success.prepared = prepared
				referenceRunStep(t, p.ctx, actor, p.postgresRecord, success)
				referenceRunStep(t, p.ctx, admin, p.postgresRecord, referenceSelect("SELECT id,i,b FROM t ORDER BY id", []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(2), int64(-7), int64(-7)}, {int64(3), nil, nil}}))
			})
		}
	}
}
