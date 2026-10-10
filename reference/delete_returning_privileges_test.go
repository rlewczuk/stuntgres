package reference_test

import "testing"

func TestReferenceDeleteReturningPrivileges(t *testing.T) {
	roleReferenceCleanup(t, []string{"returning_reader"})
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
	t.Run(backend.name, func(t *testing.T) {
		a, owner := referenceLockConnections(t, p.ctx, backend.db)
		r := backend.recorder
		if backend.pg { referenceLockExec(t,p.ctx,a,"GRANT USAGE ON SCHEMA public TO PUBLIC",0) }
		for _, q := range []string{"CREATE USER returning_reader", "CREATE TABLE t(id INT,v TEXT)", "GRANT DELETE ON t TO returning_reader"} {
			referenceLockExec(t, p.ctx, a, q, 0)
		}
		referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1,'a'),(2,'b')", 2)
		referenceLockExec(t, p.ctx, a, "SET SESSION AUTHORIZATION returning_reader", 0)
		referenceRunStep(t, p.ctx, a, r, referenceSelect("DELETE FROM t WHERE FALSE RETURNING 1", []string{"?column?"}, []string{"INT4"}, [][]any{}))
		denied := func(q string) {
			t.Helper()
			for _, prepared := range []bool{false, true} {
				s := referenceImplicitError(q, referenceDiagnostic{Code: "42501", Message: "permission denied for table t"})
				s.prepared, s.selectRows, s.errorStage = prepared, true, "execute"
				referenceRunStep(t, p.ctx, a, r, s)
			}
		}
		for _, q := range []string{"DELETE FROM t RETURNING id", "DELETE FROM t RETURNING old.id", "DELETE FROM t RETURNING new.id", "DELETE FROM t RETURNING WITH (NEW AS n) n.id", "DELETE FROM t RETURNING CASE WHEN FALSE THEN v ELSE 'safe' END"} {
			denied(q)
		}
		referenceLockExec(t, p.ctx, a, "RESET SESSION AUTHORIZATION", 0)
		referenceLockExec(t, p.ctx, a, "GRANT SELECT(id) ON t TO returning_reader", 0)
		deleteReferenceRows(t, p, a, r, 1, 2)
		referenceLockExec(t, p.ctx, a, "SET SESSION AUTHORIZATION returning_reader", 0)
		for _, q := range []string{"DELETE FROM t WHERE FALSE RETURNING id", "DELETE FROM t WHERE FALSE RETURNING old.id", "DELETE FROM t WHERE FALSE RETURNING new.id"} {
			referenceRunStep(t, p.ctx, a, r, referenceSelect(q, []string{"id"}, []string{"INT4"}, [][]any{}))
		}
		for _, q := range []string{"DELETE FROM t RETURNING v", "DELETE FROM t RETURNING *", "DELETE FROM t RETURNING old.*", "DELETE FROM t RETURNING new.*"} {
			denied(q)
		}
		query := "DELETE FROM t WHERE id=$1 RETURNING new.id"
		held, err := a.PrepareContext(p.ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		s := referenceSelect(query, []string{"id"}, []string{"INT4"}, [][]any{{nil}})
		s.args = []any{2}
		referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: a, statement: held, query: query}, r, s)
		referenceLockExec(t, p.ctx, a, "RESET SESSION AUTHORIZATION", 0)
		referenceLockExec(t, p.ctx, a, "REVOKE SELECT(id) ON t FROM returning_reader", 0)
		referenceLockExec(t, p.ctx, a, "SET SESSION AUTHORIZATION returning_reader", 0)
		s = referenceImplicitError(query, referenceDiagnostic{Code: "42501", Message: "permission denied for table t"}, 1)
		s.selectRows, s.errorStage = true, "execute"
		referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: a, statement: held, query: query}, r, s)
		referenceLockExec(t, p.ctx, a, "RESET SESSION AUTHORIZATION", 0)
		deleteReferenceRows(t, p, a, r, 1)
		referenceLockExec(t, p.ctx, a, "SET SESSION AUTHORIZATION returning_reader", 0)
		tx, err := a.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		referenceRunStep(t, p.ctx, tx, r, referenceSelect("DELETE FROM t RETURNING 1", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(1)}}))
		referenceLockExec(t, p.ctx, owner, "REVOKE DELETE ON t FROM returning_reader", 0)
		if err = tx.Commit(); err != nil {
			t.Fatal("poststage ACL revision vetoed COMMIT", err)
		}
		referenceLockExec(t, p.ctx, a, "RESET SESSION AUTHORIZATION", 0)
		deleteReferenceRows(t, p, a, r)
	})
	}
}
