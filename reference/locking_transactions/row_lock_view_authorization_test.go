package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRowLockViewAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"row_lock_view_owner", "row_lock_view_reader"})
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		admin, actor := fixture.LockConnections(t, p.Ctx, backend.DB)
		const owner = "row_lock_view_owner"
		const reader = "row_lock_view_reader"
		if backend.PG {
			fixture.LockExec(t, p.Ctx, admin, "GRANT USAGE,CREATE ON SCHEMA public TO PUBLIC", 0)
		}
		for _, q := range []string{"CREATE ROLE " + owner, "CREATE ROLE " + reader, "CREATE TABLE t(id INT,v INT)", "INSERT INTO t VALUES(1,10)"} {
			if _, err := admin.ExecContext(p.Ctx, q); err != nil {
				t.Fatal(err)
			}
		}
		t.Cleanup(func() {
			actor.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			admin.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		})
		fixture.LockExec(t, p.Ctx, admin, "SET SESSION AUTHORIZATION "+owner, 0)
		// PostgreSQL independently permits definition without source SELECT or UPDATE.
		for _, q := range []string{"CREATE VIEW owned AS SELECT id FROM t", "CREATE VIEW invoker WITH(security_invoker=true) AS SELECT id FROM t", "CREATE VIEW nested AS SELECT id FROM invoker", "CREATE VIEW stored AS SELECT id FROM t FOR UPDATE"} {
			fixture.LockExec(t, p.Ctx, admin, q, 0)
		}
		fixture.LockExec(t, p.Ctx, admin, "RESET SESSION AUTHORIZATION", 0)
		fixture.LockExec(t, p.Ctx, admin, "GRANT SELECT(id) ON t TO "+owner, 0)
		fixture.LockExec(t, p.Ctx, admin, "GRANT SELECT,UPDATE ON owned,invoker,nested,stored TO "+reader, 0)
		fixture.LockExec(t, p.Ctx, actor, "SET SESSION AUTHORIZATION "+reader, 0)
		denied := func(query string, prepared bool) {
			t.Helper()
			step := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for table t"})
			step.SelectRows, step.Prepared, step.ErrorStage = true, prepared, "execute"
			fixture.RunStep(t, p.Ctx, actor, backend.Recorder, step)
		}
		selected := func(query string, prepared bool) {
			t.Helper()
			step := fixture.Select(query, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}})
			step.Prepared = prepared
			fixture.RunStep(t, p.Ctx, actor, backend.Recorder, step)
		}
		for _, clause := range fixture.RowClauses {
			for _, prepared := range []bool{false, true} {
				denied("SELECT id FROM owned "+clause, prepared)
			}
		}
		denied("SELECT id FROM stored", true)
		fixture.LockExec(t, p.Ctx, admin, "GRANT UPDATE(v) ON t TO "+owner, 0)
		// Caller has no base privileges: owner-view descendant checks use the owner.
		for _, clause := range fixture.RowClauses {
			for _, prepared := range []bool{false, true} {
				selected("SELECT id FROM owned "+clause, prepared)
			}
		}
		fixture.LockExec(t, p.Ctx, admin, "REVOKE UPDATE ON stored FROM "+reader, 0)
		selected("SELECT id FROM stored", true)
		fixture.LockExec(t, p.Ctx, admin, "GRANT SELECT(id) ON t TO "+reader, 0)
		for _, name := range []string{"invoker", "nested"} {
			for _, clause := range fixture.RowClauses {
				denied("SELECT id FROM "+name+" "+clause, true)
			}
		}
		fixture.LockExec(t, p.Ctx, admin, "GRANT UPDATE(v) ON t TO "+reader, 0)
		for _, name := range []string{"invoker", "nested"} {
			for _, clause := range fixture.RowClauses {
				selected("SELECT id FROM "+name+" "+clause, true)
			}
		}
		fixture.LockExec(t, p.Ctx, admin, "REVOKE UPDATE(v) ON t FROM "+owner, 0)
		denied("SELECT id FROM owned FOR UPDATE", true)
		fixture.LockExec(t, p.Ctx, actor, "RESET SESSION AUTHORIZATION", 0)
	}
}

func TestReferenceRowLockViewSequenceAuthorization(t *testing.T) {
	if !*fixture.Mode {
		return
	} // Adjacent sequence privileges remain oracle-only.
	p := fixture.NewPair(t)
	admin, actor := fixture.LockConnections(t, p.Ctx, p.Postgres)
	const role = "row_lock_view_sequence_reader"
	for _, q := range []string{"CREATE ROLE " + role, "GRANT USAGE ON SCHEMA public TO " + role, "CREATE SEQUENCE s", "CREATE VIEW v WITH(security_invoker=true) AS SELECT last_value FROM s", "GRANT SELECT ON s TO " + role, "GRANT SELECT,UPDATE ON v TO " + role} {
		fixture.LockExec(t, p.Ctx, admin, q, 0)
	}
	t.Cleanup(func() {
		actor.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		for _, q := range []string{"DROP OWNED BY " + role + " CASCADE", "DROP ROLE " + role} {
			if _, err := admin.ExecContext(p.Ctx, q); err != nil {
				t.Error(q, err)
			}
		}
	})
	fixture.LockExec(t, p.Ctx, actor, "SET SESSION AUTHORIZATION "+role, 0)
	for _, clause := range fixture.RowClauses {
		for _, prepared := range []bool{false, true} {
			failure := fixture.ImplicitError("SELECT last_value FROM v "+clause, fixture.Diagnostic{Code: "42501", Message: "permission denied for sequence s"})
			failure.SelectRows, failure.Prepared, failure.ErrorStage = true, prepared, "execute"
			fixture.RunStep(t, p.Ctx, actor, p.PostgresRecord, failure)
		}
	}
	fixture.LockExec(t, p.Ctx, actor, "RESET SESSION AUTHORIZATION", 0)
}
