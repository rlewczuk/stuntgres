//go:build reference

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDeleteCoreExpressionACLPrecedence(t *testing.T) {
	const role = "delete_expression_actor"
	fixture.RoleReferenceCleanup(t, []string{role})
	p := fixture.NewPair(t)
	admin, actor := fixture.LockConnections(t, p.Ctx, p.Postgres)
	t.Cleanup(func() {
		fixture.RunStep(t, p.Ctx, admin, p.PostgresRecord, fixture.Exec("DROP SCHEMA IF EXISTS hidden CASCADE", 0))
	})
	for _, query := range []string{"CREATE USER " + role, "CREATE TABLE t(i INT,s TEXT)", "INSERT INTO t VALUES(1,'oops')", "GRANT USAGE ON SCHEMA public TO " + role, "CREATE SCHEMA hidden", "CREATE TABLE hidden.t(i INT)"} {
		count := int64(0)
		if query == "INSERT INTO t VALUES(1,'oops')" {
			count = 1
		}
		fixture.RunStep(t, p.Ctx, admin, p.PostgresRecord, fixture.Exec(query, count))
	}
	fixture.RunStep(t, p.Ctx, actor, p.PostgresRecord, fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
	t.Cleanup(func() {
		fixture.RunStep(t, p.Ctx, actor, p.PostgresRecord, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
	for _, tc := range []struct {
		query string
		args  []any
		want  fixture.Diagnostic
	}{
		{"DELETE FROM t WHERE i=int4($1::text)", []any{"oops"}, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}},
		{"DELETE FROM t WHERE i=int4($1::text)", []any{"1"}, fixture.Diagnostic{Code: "42501", Message: "permission denied for table t"}},
		{"DELETE FROM t WHERE int4(s) IS NULL", nil, fixture.Diagnostic{Code: "42501", Message: "permission denied for table t"}},
		{"DELETE FROM hidden.t WHERE i=int4($1::text)", []any{"oops"}, fixture.Diagnostic{Code: "42501", Message: "permission denied for schema hidden", Position: "13"}},
	} {
		for _, prepared := range []bool{false, true} {
			step := fixture.ImplicitError(tc.query, tc.want, tc.args...)
			step.Prepared, step.ErrorStage = prepared, "execute"
			if prepared && tc.want.Message == "permission denied for schema hidden" {
				step.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, actor, p.PostgresRecord, step)
			fixture.RunStep(t, p.Ctx, admin, p.PostgresRecord, fixture.Select("SELECT i FROM t", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}}))
		}
	}
}
