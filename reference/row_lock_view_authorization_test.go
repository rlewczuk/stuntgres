//go:build reference

package reference_test

import "testing"

func TestReferenceRowLockViewAuthorization(t *testing.T) {
	p := newReferencePair(t)
	admin, actor := referenceLockConnections(t, p.ctx, p.postgres)
	const owner = "row_lock_view_owner"
	const reader = "row_lock_view_reader"
	for _, q := range []string{"CREATE ROLE " + owner, "CREATE ROLE " + reader, "GRANT USAGE,CREATE ON SCHEMA public TO " + owner, "GRANT USAGE ON SCHEMA public TO " + reader, "CREATE TABLE t(id INT,v INT)", "INSERT INTO t VALUES(1,10)"} {
		if _, err := admin.ExecContext(p.ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		actor.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
		for _, q := range []string{"DROP OWNED BY " + owner + "," + reader + " CASCADE", "DROP ROLE " + owner + "," + reader} {
			if _, err := admin.ExecContext(p.ctx, q); err != nil {
				t.Error(q, err)
			}
		}
	})
	referenceLockExec(t, p.ctx, admin, "SET SESSION AUTHORIZATION "+owner, 0)
	// PostgreSQL independently permits definition without source SELECT or UPDATE.
	for _, q := range []string{"CREATE VIEW owned AS SELECT id FROM t", "CREATE VIEW invoker WITH(security_invoker=true) AS SELECT id FROM t", "CREATE VIEW nested AS SELECT id FROM invoker", "CREATE VIEW stored AS SELECT id FROM t FOR UPDATE"} {
		referenceLockExec(t, p.ctx, admin, q, 0)
	}
	referenceLockExec(t, p.ctx, admin, "RESET SESSION AUTHORIZATION", 0)
	referenceLockExec(t, p.ctx, admin, "GRANT SELECT(id) ON t TO "+owner, 0)
	referenceLockExec(t, p.ctx, admin, "GRANT SELECT,UPDATE ON owned,invoker,nested,stored TO "+reader, 0)
	referenceLockExec(t, p.ctx, actor, "SET SESSION AUTHORIZATION "+reader, 0)
	denied := func(query string, prepared bool) {
		t.Helper()
		step := referenceImplicitError(query, referenceDiagnostic{Code: "42501", Message: "permission denied for table t"})
		step.selectRows, step.prepared, step.errorStage = true, prepared, "execute"
		referenceRunStep(t, p.ctx, actor, p.postgresRecord, step)
	}
	selected := func(query string, prepared bool) {
		t.Helper()
		step := referenceSelect(query, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}})
		step.prepared = prepared
		referenceRunStep(t, p.ctx, actor, p.postgresRecord, step)
	}
	for _, clause := range referenceRowClauses {
		for _, prepared := range []bool{false, true} {
			denied("SELECT id FROM owned "+clause, prepared)
		}
	}
	denied("SELECT id FROM stored", true)
	referenceLockExec(t, p.ctx, admin, "GRANT UPDATE(v) ON t TO "+owner, 0)
	// Caller has no base privileges: owner-view descendant checks use the owner.
	for _, clause := range referenceRowClauses {
		for _, prepared := range []bool{false, true} {
			selected("SELECT id FROM owned "+clause, prepared)
		}
	}
	referenceLockExec(t, p.ctx, admin, "REVOKE UPDATE ON stored FROM "+reader, 0)
	selected("SELECT id FROM stored", true)
	referenceLockExec(t, p.ctx, admin, "GRANT SELECT(id) ON t TO "+reader, 0)
	for _, name := range []string{"invoker", "nested"} {
		for _, clause := range referenceRowClauses {
			denied("SELECT id FROM "+name+" "+clause, true)
		}
	}
	referenceLockExec(t, p.ctx, admin, "GRANT UPDATE(v) ON t TO "+reader, 0)
	for _, name := range []string{"invoker", "nested"} {
		for _, clause := range referenceRowClauses {
			selected("SELECT id FROM "+name+" "+clause, true)
		}
	}
	referenceLockExec(t, p.ctx, admin, "REVOKE UPDATE(v) ON t FROM "+owner, 0)
	denied("SELECT id FROM owned FOR UPDATE", true)
	// Role-specific controls are not sent through the native SQL GRANT surface;
	// engine ViewAuthorization uses the existing object/column ACL API independently.
	referenceLockExec(t, p.ctx, actor, "RESET SESSION AUTHORIZATION", 0)
}

func TestReferenceRowLockViewSequenceAuthorization(t *testing.T) {
	p := newReferencePair(t)
	admin, actor := referenceLockConnections(t, p.ctx, p.postgres)
	const role = "row_lock_view_sequence_reader"
	for _, q := range []string{"CREATE ROLE " + role, "GRANT USAGE ON SCHEMA public TO " + role, "CREATE SEQUENCE s", "CREATE VIEW v WITH(security_invoker=true) AS SELECT last_value FROM s", "GRANT SELECT ON s TO " + role, "GRANT SELECT,UPDATE ON v TO " + role} {
		referenceLockExec(t, p.ctx, admin, q, 0)
	}
	t.Cleanup(func() {
		actor.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
		for _, q := range []string{"DROP OWNED BY " + role + " CASCADE", "DROP ROLE " + role} {
			if _, err := admin.ExecContext(p.ctx, q); err != nil {
				t.Error(q, err)
			}
		}
	})
	referenceLockExec(t, p.ctx, actor, "SET SESSION AUTHORIZATION "+role, 0)
	for _, clause := range referenceRowClauses {
		for _, prepared := range []bool{false, true} {
			failure := referenceImplicitError("SELECT last_value FROM v "+clause, referenceDiagnostic{Code: "42501", Message: "permission denied for sequence s"})
			failure.selectRows, failure.prepared, failure.errorStage = true, prepared, "execute"
			referenceRunStep(t, p.ctx, actor, p.postgresRecord, failure)
		}
	}
	referenceLockExec(t, p.ctx, actor, "RESET SESSION AUTHORIZATION", 0)
}
