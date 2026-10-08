//go:build reference

package reference_test

import "testing"

func TestReferenceDeleteCoreExpressionACLPrecedence(t *testing.T) {
	const role = "delete_expression_actor"
	roleReferenceCleanup(t, []string{role})
	p := newReferencePair(t)
	admin, actor := referenceLockConnections(t, p.ctx, p.postgres)
	t.Cleanup(func() {
		referenceRunStep(t, p.ctx, admin, p.postgresRecord, referenceExec("DROP SCHEMA IF EXISTS hidden CASCADE", 0))
	})
	for _, query := range []string{"CREATE USER " + role, "CREATE TABLE t(i INT,s TEXT)", "INSERT INTO t VALUES(1,'oops')", "GRANT USAGE ON SCHEMA public TO " + role, "CREATE SCHEMA hidden", "CREATE TABLE hidden.t(i INT)"} {
		count := int64(0)
		if query == "INSERT INTO t VALUES(1,'oops')" {
			count = 1
		}
		referenceRunStep(t, p.ctx, admin, p.postgresRecord, referenceExec(query, count))
	}
	referenceRunStep(t, p.ctx, actor, p.postgresRecord, referenceExec("SET SESSION AUTHORIZATION "+role, 0))
	t.Cleanup(func() {
		referenceRunStep(t, p.ctx, actor, p.postgresRecord, referenceExec("RESET SESSION AUTHORIZATION", 0))
	})
	for _, tc := range []struct {
		query string
		args  []any
		want  referenceDiagnostic
	}{
		{"DELETE FROM t WHERE i=int4($1::text)", []any{"oops"}, referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}},
		{"DELETE FROM t WHERE i=int4($1::text)", []any{"1"}, referenceDiagnostic{Code: "42501", Message: "permission denied for table t"}},
		{"DELETE FROM t WHERE int4(s) IS NULL", nil, referenceDiagnostic{Code: "42501", Message: "permission denied for table t"}},
		{"DELETE FROM hidden.t WHERE i=int4($1::text)", []any{"oops"}, referenceDiagnostic{Code: "42501", Message: "permission denied for schema hidden", Position: "13"}},
	} {
		for _, prepared := range []bool{false, true} {
			step := referenceImplicitError(tc.query, tc.want, tc.args...)
			step.prepared, step.errorStage = prepared, "execute"
			if prepared && tc.want.Message == "permission denied for schema hidden" {
				step.errorStage = "prepare"
			}
			referenceRunStep(t, p.ctx, actor, p.postgresRecord, step)
			referenceRunStep(t, p.ctx, admin, p.postgresRecord, referenceSelect("SELECT i FROM t", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}}))
		}
	}
}
