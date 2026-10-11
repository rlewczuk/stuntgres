package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateReturningPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"update_returning_reader"})
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			a, owner := fixture.LockConnections(t, p.Ctx, backend.DB)
			r := backend.Recorder
			if backend.PG {
				fixture.LockExec(t, p.Ctx, a, "GRANT USAGE ON SCHEMA public TO PUBLIC", 0)
			}
			for _, q := range []string{"CREATE USER update_returning_reader", "CREATE TABLE t(id INT,v TEXT)", "GRANT UPDATE(id) ON t TO update_returning_reader"} {
				fixture.LockExec(t, p.Ctx, a, q, 0)
			}
			fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(1,'a'),(2,'b')", 2)
			fixture.LockExec(t, p.Ctx, a, "SET SESSION AUTHORIZATION update_returning_reader", 0)
			fixture.RunStep(t, p.Ctx, a, r, fixture.Select("UPDATE t SET id=7 WHERE FALSE RETURNING 1", []string{"?column?"}, []string{"INT4"}, [][]any{}))
			denied := func(q string) {
				t.Helper()
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table t"})
					s.Prepared, s.SelectRows, s.ErrorStage = prepared, true, "execute"
					fixture.RunStep(t, p.Ctx, a, r, s)
				}
			}
			for _, q := range []string{"UPDATE t SET id=7 RETURNING id", "UPDATE t SET id=7 RETURNING old.id", "UPDATE t SET id=7 RETURNING new.id", "UPDATE t SET id=7 RETURNING WITH (NEW AS n) n.id", "UPDATE t SET id=7 RETURNING CASE WHEN FALSE THEN v ELSE 'safe' END"} {
				denied(q)
			}
			fixture.LockExec(t, p.Ctx, a, "RESET SESSION AUTHORIZATION", 0)
			fixture.LockExec(t, p.Ctx, a, "GRANT SELECT(id) ON t TO update_returning_reader", 0)
			fixture.DeleteReferenceRows(t, p, a, r, 1, 2)
			fixture.LockExec(t, p.Ctx, a, "SET SESSION AUTHORIZATION update_returning_reader", 0)
			for _, q := range []string{"UPDATE t SET id=7 WHERE FALSE RETURNING id", "UPDATE t SET id=7 WHERE FALSE RETURNING old.id", "UPDATE t SET id=7 WHERE FALSE RETURNING new.id"} {
				fixture.RunStep(t, p.Ctx, a, r, fixture.Select(q, []string{"id"}, []string{"INT4"}, [][]any{}))
			}
			for _, q := range []string{"UPDATE t SET id=7 RETURNING v", "UPDATE t SET id=7 RETURNING *", "UPDATE t SET id=7 RETURNING old.*", "UPDATE t SET id=7 RETURNING new.*"} {
				denied(q)
			}
			query := "UPDATE t SET id=7 WHERE id=$1 RETURNING new.id"
			held, err := a.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			s := fixture.Select(query, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
			s.Args = []any{2}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: a, Statement: held, SQL: query}, r, s)
			fixture.LockExec(t, p.Ctx, a, "RESET SESSION AUTHORIZATION", 0)
			fixture.LockExec(t, p.Ctx, a, "REVOKE SELECT(id) ON t FROM update_returning_reader", 0)
			fixture.LockExec(t, p.Ctx, a, "SET SESSION AUTHORIZATION update_returning_reader", 0)
			s = fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for table t"}, 1)
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: a, Statement: held, SQL: query}, r, s)
			fixture.LockExec(t, p.Ctx, a, "RESET SESSION AUTHORIZATION", 0)
			fixture.DeleteReferenceRows(t, p, a, r, 1, 7)
			fixture.LockExec(t, p.Ctx, a, "SET SESSION AUTHORIZATION update_returning_reader", 0)
			tx, err := a.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fixture.RunStep(t, p.Ctx, tx, r, fixture.Select("UPDATE t SET id=7 RETURNING 1", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}}))
			fixture.LockExec(t, p.Ctx, owner, "REVOKE UPDATE(id) ON t FROM update_returning_reader", 0)
			if err = tx.Commit(); err != nil {
				t.Fatal("poststage ACL revision vetoed COMMIT", err)
			}
			fixture.LockExec(t, p.Ctx, a, "RESET SESSION AUTHORIZATION", 0)
			fixture.DeleteReferenceRows(t, p, a, r, 7, 7)
		})
	}
}
