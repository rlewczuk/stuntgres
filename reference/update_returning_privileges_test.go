//go:build reference

package reference_test

import "testing"

func TestReferenceUpdateReturningPrivileges(t *testing.T) {
	roleReferenceCleanup(t, []string{"update_returning_reader"})
	p := newReferencePair(t)
	t.Run("postgres", func(t *testing.T) {
		a, owner := referenceLockConnections(t, p.ctx, p.postgres)
		r := p.postgresRecord
		for _, q := range []string{"CREATE USER update_returning_reader", "CREATE TABLE t(id INT,v TEXT)", "GRANT USAGE ON SCHEMA public TO update_returning_reader", "GRANT UPDATE(id) ON t TO update_returning_reader"} {
			referenceLockExec(t, p.ctx, a, q, 0)
		}
		referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1,'a'),(2,'b')", 2)
		referenceLockExec(t, p.ctx, a, "SET SESSION AUTHORIZATION update_returning_reader", 0)
		referenceRunStep(t, p.ctx, a, r, referenceSelect("UPDATE t SET id=7 WHERE FALSE RETURNING 1", []string{"?column?"}, []string{"INT4"}, [][]any{}))
		denied := func(q string) {
			t.Helper()
			for _, prepared := range []bool{false, true} {
				s := referenceImplicitError(q, referenceDiagnostic{Code: "42501", Message: "permission denied for table t"})
				s.prepared, s.selectRows, s.errorStage = prepared, true, "execute"
				referenceRunStep(t, p.ctx, a, r, s)
			}
		}
		for _, q := range []string{"UPDATE t SET id=7 RETURNING id", "UPDATE t SET id=7 RETURNING old.id", "UPDATE t SET id=7 RETURNING new.id", "UPDATE t SET id=7 RETURNING WITH (NEW AS n) n.id", "UPDATE t SET id=7 RETURNING CASE WHEN FALSE THEN v ELSE 'safe' END"} {
			denied(q)
		}
		referenceLockExec(t, p.ctx, a, "RESET SESSION AUTHORIZATION", 0)
		referenceLockExec(t, p.ctx, a, "GRANT SELECT(id) ON t TO update_returning_reader", 0)
		deleteReferenceRows(t, p, a, r, 1, 2)
		referenceLockExec(t, p.ctx, a, "SET SESSION AUTHORIZATION update_returning_reader", 0)
		for _, q := range []string{"UPDATE t SET id=7 WHERE FALSE RETURNING id", "UPDATE t SET id=7 WHERE FALSE RETURNING old.id", "UPDATE t SET id=7 WHERE FALSE RETURNING new.id"} {
			referenceRunStep(t, p.ctx, a, r, referenceSelect(q, []string{"id"}, []string{"INT4"}, [][]any{}))
		}
		for _, q := range []string{"UPDATE t SET id=7 RETURNING v", "UPDATE t SET id=7 RETURNING *", "UPDATE t SET id=7 RETURNING old.*", "UPDATE t SET id=7 RETURNING new.*"} {
			denied(q)
		}
		query := "UPDATE t SET id=7 WHERE id=$1 RETURNING new.id"
		held, err := a.PrepareContext(p.ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		s := referenceSelect(query, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
		s.args = []any{2}
		referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: a, statement: held, query: query}, r, s)
		referenceLockExec(t, p.ctx, a, "RESET SESSION AUTHORIZATION", 0)
		referenceLockExec(t, p.ctx, a, "REVOKE SELECT(id) ON t FROM update_returning_reader", 0)
		referenceLockExec(t, p.ctx, a, "SET SESSION AUTHORIZATION update_returning_reader", 0)
		s = referenceImplicitError(query, referenceDiagnostic{Code: "42501", Message: "permission denied for table t"}, 1)
		s.selectRows, s.errorStage = true, "execute"
		referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: a, statement: held, query: query}, r, s)
		referenceLockExec(t, p.ctx, a, "RESET SESSION AUTHORIZATION", 0)
		deleteReferenceRows(t, p, a, r, 1, 7)
		referenceLockExec(t, p.ctx, a, "SET SESSION AUTHORIZATION update_returning_reader", 0)
		tx, err := a.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		referenceRunStep(t, p.ctx, tx, r, referenceSelect("UPDATE t SET id=7 RETURNING 1", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}}))
		referenceLockExec(t, p.ctx, owner, "REVOKE UPDATE(id) ON t FROM update_returning_reader", 0)
		if err = tx.Commit(); err != nil {
			t.Fatal("poststage ACL revision vetoed COMMIT", err)
		}
		referenceLockExec(t, p.ctx, a, "RESET SESSION AUTHORIZATION", 0)
		deleteReferenceRows(t, p, a, r, 7, 7)
	})
}
