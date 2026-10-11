package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewUpdatableSecurity(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(fmt.Sprint(held), func(t *testing.T) {
			actor := fmt.Sprintf("view_writer_%v", held)
			owner := fmt.Sprintf("view_write_owner_%v", held)
			fixture.RoleReferenceCleanup(t, []string{actor, owner})
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					run := func(s fixture.Step) { s.Prepared = held; fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					admin := func(q string) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0)) }
					for _, q := range []string{"CREATE USER " + actor, "CREATE USER " + owner, "CREATE TABLE t(id INT,a INT,hidden INT)", "CREATE VIEW v AS SELECT id,a FROM t WHERE hidden>0", "CREATE VIEW inv WITH(security_invoker=true) AS SELECT id,a FROM t", "CREATE VIEW outer_inv AS SELECT id,a FROM inv"} {
						admin(q)
					}
					// Fixture reset creates public without its initdb PUBLIC schema privileges.
					// Native schema ACLs remain the existing boundary; normalize PG lookup here,
					// independently of the view/base relation and column rights under test.
					if b.PG {
						admin("GRANT USAGE ON SCHEMA public TO " + actor)
						admin("GRANT USAGE,CREATE ON SCHEMA public TO " + owner)
					}
					run(fixture.Exec("INSERT INTO t VALUES(1,10,1),(2,20,0)", 2))
					admin("GRANT SELECT,INSERT,UPDATE,DELETE ON v,inv,outer_inv TO " + actor)
					run(fixture.Exec("SET SESSION AUTHORIZATION "+actor, 0))
					run(fixture.Exec("INSERT INTO v VALUES(3,30)", 1))
					run(fixture.Exec("DELETE FROM v WHERE id=3", 0)) // INSERT is not constrained by the view's hidden predicate.
					run(fixture.ImplicitError("DELETE FROM inv WHERE id=99", fixture.Diagnostic{Code: "42501", Message: "permission denied for table t"}))
					admin("REVOKE INSERT,DELETE ON v FROM " + actor)
					run(fixture.ImplicitError("INSERT INTO v SELECT 99,99 WHERE FALSE", fixture.Diagnostic{Code: "42501", Message: "permission denied for view v"}))
					run(fixture.ImplicitError("DELETE FROM v WHERE FALSE", fixture.Diagnostic{Code: "42501", Message: "permission denied for view v"}))
					admin("GRANT INSERT,DELETE ON v TO " + actor)
					q := "UPDATE v SET a=$1 WHERE id=1"
					var executor fixture.Executor = c
					if held {
						stmt, err := c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						executor = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
					}
					write := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, executor, b.Recorder, s) }
					write(fixture.Exec(q, 1, 11))
					admin("REVOKE UPDATE ON v FROM " + actor)
					denied := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for view v"}, 12)
					denied.ErrorStage = "execute"
					write(denied)
					admin("GRANT UPDATE(a) ON v TO " + actor)
					write(fixture.Exec(q, 1, 13))
					run(fixture.ImplicitError("UPDATE inv SET a=99 WHERE FALSE", fixture.Diagnostic{Code: "42501", Message: "permission denied for table t"}))
					run(fixture.ImplicitError("UPDATE outer_inv SET a=99 WHERE id=1", fixture.Diagnostic{Code: "42501", Message: "permission denied for table t"}))
					admin("GRANT SELECT(id,a,hidden),UPDATE(a) ON t TO " + actor)
					run(fixture.Exec("UPDATE outer_inv SET a=14 WHERE id=1", 1))
					run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
					admin("GRANT SELECT ON t TO " + owner)
					admin("ALTER VIEW v OWNER TO " + owner)
					run(fixture.Exec("SET SESSION AUTHORIZATION "+actor, 0))
					write(fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table t"}, 15))
					admin("GRANT UPDATE(a) ON t TO " + owner)
					write(fixture.Exec(q, 1, 16))
					admin("ALTER VIEW v SET(security_invoker=true)")
					admin("REVOKE UPDATE(a) ON t FROM " + actor)
					write(fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table t"}, 17))
					admin("ALTER VIEW v RESET(security_invoker)")
					write(fixture.Exec(q, 1, 18))
					run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
					run(fixture.Select("SELECT id,a,hidden FROM t ORDER BY id", []string{"id", "a", "hidden"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(18), int64(1)}, {int64(2), int64(20), int64(0)}, {int64(3), int64(30), nil}}))
				})
			}
		})
	}
}
