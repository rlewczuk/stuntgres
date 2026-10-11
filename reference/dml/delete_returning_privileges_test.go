package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDeleteReturningPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"returning_reader"})
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			a, owner := fixture.LockConnections(t, p.Ctx, backend.DB)
			r := backend.Recorder
			if backend.PG {
				fixture.LockExec(t, p.Ctx, a, "GRANT USAGE ON SCHEMA public TO PUBLIC", 0)
			}
			for _, q := range []string{"CREATE USER returning_reader", "CREATE TABLE t(id INT,v TEXT)", "GRANT DELETE ON t TO returning_reader"} {
				fixture.LockExec(t, p.Ctx, a, q, 0)
			}
			fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(1,'a'),(2,'b')", 2)
			fixture.LockExec(t, p.Ctx, a, "SET SESSION AUTHORIZATION returning_reader", 0)
			fixture.RunStep(t, p.Ctx, a, r, fixture.Select("DELETE FROM t WHERE FALSE RETURNING 1", []string{"?column?"}, []string{"INT4"}, [][]any{}))
			denied := func(q string) {
				t.Helper()
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table t"})
					s.Prepared, s.SelectRows, s.ErrorStage = prepared, true, "execute"
					fixture.RunStep(t, p.Ctx, a, r, s)
				}
			}
			for _, q := range []string{"DELETE FROM t RETURNING id", "DELETE FROM t RETURNING old.id", "DELETE FROM t RETURNING new.id", "DELETE FROM t RETURNING WITH (NEW AS n) n.id", "DELETE FROM t RETURNING CASE WHEN FALSE THEN v ELSE 'safe' END"} {
				denied(q)
			}
			fixture.LockExec(t, p.Ctx, a, "RESET SESSION AUTHORIZATION", 0)
			fixture.LockExec(t, p.Ctx, a, "GRANT SELECT(id) ON t TO returning_reader", 0)
			fixture.DeleteReferenceRows(t, p, a, r, 1, 2)
			fixture.LockExec(t, p.Ctx, a, "SET SESSION AUTHORIZATION returning_reader", 0)
			for _, q := range []string{"DELETE FROM t WHERE FALSE RETURNING id", "DELETE FROM t WHERE FALSE RETURNING old.id", "DELETE FROM t WHERE FALSE RETURNING new.id"} {
				fixture.RunStep(t, p.Ctx, a, r, fixture.Select(q, []string{"id"}, []string{"INT4"}, [][]any{}))
			}
			for _, q := range []string{"DELETE FROM t RETURNING v", "DELETE FROM t RETURNING *", "DELETE FROM t RETURNING old.*", "DELETE FROM t RETURNING new.*"} {
				denied(q)
			}
			query := "DELETE FROM t WHERE id=$1 RETURNING new.id"
			held, err := a.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			s := fixture.Select(query, []string{"id"}, []string{"INT4"}, [][]any{{nil}})
			s.Args = []any{2}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: a, Statement: held, SQL: query}, r, s)
			fixture.LockExec(t, p.Ctx, a, "RESET SESSION AUTHORIZATION", 0)
			fixture.LockExec(t, p.Ctx, a, "REVOKE SELECT(id) ON t FROM returning_reader", 0)
			fixture.LockExec(t, p.Ctx, a, "SET SESSION AUTHORIZATION returning_reader", 0)
			s = fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for table t"}, 1)
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: a, Statement: held, SQL: query}, r, s)
			fixture.LockExec(t, p.Ctx, a, "RESET SESSION AUTHORIZATION", 0)
			fixture.DeleteReferenceRows(t, p, a, r, 1)
			fixture.LockExec(t, p.Ctx, a, "SET SESSION AUTHORIZATION returning_reader", 0)
			tx, err := a.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fixture.RunStep(t, p.Ctx, tx, r, fixture.Select("DELETE FROM t RETURNING 1", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(1)}}))
			fixture.LockExec(t, p.Ctx, owner, "REVOKE DELETE ON t FROM returning_reader", 0)
			if err = tx.Commit(); err != nil {
				t.Fatal("poststage ACL revision vetoed COMMIT", err)
			}
			fixture.LockExec(t, p.Ctx, a, "RESET SESSION AUTHORIZATION", 0)
			fixture.DeleteReferenceRows(t, p, a, r)
		})
	}
}
