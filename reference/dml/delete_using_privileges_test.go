package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDeleteUsingPrivilegeOracle(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"delete_using_reader"})
	deleteUsingReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := updateFromRun(t, p, b, c)
		run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
		run(fixture.Exec("CREATE TABLE s(k INT,label TEXT)", 0))
		run(fixture.Exec("CREATE TABLE r(k INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,10),(2,20)", 2))
		run(fixture.Exec("INSERT INTO s VALUES(1,'one')", 1))
		run(fixture.Exec("INSERT INTO r VALUES(1)", 1))
		run(fixture.Exec("CREATE USER delete_using_reader", 0))
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO delete_using_reader", 0))
		}
		run(fixture.Exec("GRANT DELETE ON t TO delete_using_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION delete_using_reader", 0))
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		for _, q := range []string{"DELETE FROM t USING s WHERE FALSE", "DELETE FROM t USING s JOIN r ON FALSE WHERE FALSE", "DELETE FROM t USING s WHERE FALSE RETURNING 1"} {
			run(fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table s")))
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT(k) ON s TO delete_using_reader", 0))
		run(fixture.Exec("GRANT SELECT(k) ON r TO delete_using_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION delete_using_reader", 0))
		run(fixture.Exec("DELETE FROM t USING s WHERE FALSE", 0))
		for _, q := range []string{"DELETE FROM t USING s WHERE t.id=s.k", "DELETE FROM t USING s WHERE FALSE RETURNING old.id", "DELETE FROM t USING s WHERE FALSE RETURNING new.id"} {
			run(fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table t")))
		}
		run(fixture.Exec("DELETE FROM t USING s RETURNING 1", 2))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,10),(2,20)", 2))
		run(fixture.Exec("GRANT SELECT(id) ON t TO delete_using_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION delete_using_reader", 0))
		for _, q := range []string{"DELETE FROM t USING s WHERE t.id=s.k RETURNING s.label", "DELETE FROM t USING s JOIN r ON s.label='one' WHERE FALSE"} {
			run(fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table s")))
		}
		q := "DELETE FROM t USING s JOIN r USING(k) WHERE t.id=s.k RETURNING old.id,new.id,s.k"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"id", "id", "k"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), nil, int64(1)}}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
		run(fixture.Exec("REVOKE SELECT(k) ON s FROM delete_using_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION delete_using_reader", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table s")))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT(k) ON s TO delete_using_reader", 0))
		run(fixture.Exec("REVOKE DELETE ON t FROM delete_using_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION delete_using_reader", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table t")))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(deleteUsingTargets([][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
		run(fixture.Select("SELECT k,label FROM s", []string{"k", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}}))
	})
}
