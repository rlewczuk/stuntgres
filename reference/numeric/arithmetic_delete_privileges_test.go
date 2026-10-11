package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceArithmeticDeletePrivileges(t *testing.T) {
	const role = "arithmetic_delete_actor"
	for _, query := range []string{"DELETE FROM t WHERE i+1=8", "DELETE FROM t WHERE CAST(i AS bigint)+1=8", "DELETE FROM t WHERE b+i=14"} {
		for _, prepared := range []bool{false, true} {
			t.Run(query+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				fixture.RoleReferenceCleanup(t, []string{role})
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					admin, actor := fixture.LockConnections(t, p.Ctx, backend.DB)
					if backend.PG {
						fixture.RunStep(t, p.Ctx, admin, backend.Recorder, fixture.Exec("GRANT USAGE ON SCHEMA public TO PUBLIC", 0))
					}
					for _, sql := range []string{"CREATE USER " + role, "CREATE TABLE t(id INT,i INT,b BIGINT)", "GRANT DELETE ON TABLE t TO " + role} {
						fixture.RunStep(t, p.Ctx, admin, backend.Recorder, fixture.Exec(sql, 0))
					}
					fixture.RunStep(t, p.Ctx, admin, backend.Recorder, fixture.Exec("INSERT INTO t VALUES(1,7,7),(2,-7,-7),(3,NULL,NULL),(4,7,7)", 4))
					fixture.RunStep(t, p.Ctx, actor, backend.Recorder, fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
					t.Cleanup(func() {
						fixture.RunStep(t, p.Ctx, actor, backend.Recorder, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
					})
					original := fixture.Select("SELECT id,i,b FROM t ORDER BY id", []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(7), int64(7)}, {int64(2), int64(-7), int64(-7)}, {int64(3), nil, nil}, {int64(4), int64(7), int64(7)}})
					denied := fixture.ErrorDiagnostic(query, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table t"})
					denied.Prepared, denied.ErrorStage = prepared, "execute"
					fixture.RunStep(t, p.Ctx, actor, backend.Recorder, denied)
					fixture.RunStep(t, p.Ctx, admin, backend.Recorder, original)
					fixture.RunStep(t, p.Ctx, admin, backend.Recorder, fixture.Exec("GRANT SELECT(i) ON TABLE t TO "+role, 0))
					if query == "DELETE FROM t WHERE b+i=14" {
						fixture.RunStep(t, p.Ctx, actor, backend.Recorder, denied)
						fixture.RunStep(t, p.Ctx, admin, backend.Recorder, original)
						fixture.RunStep(t, p.Ctx, admin, backend.Recorder, fixture.Exec("GRANT SELECT(b) ON TABLE t TO "+role, 0))
					}
					success := fixture.Exec(query, 2)
					success.Prepared = prepared
					fixture.RunStep(t, p.Ctx, actor, backend.Recorder, success)
					fixture.RunStep(t, p.Ctx, admin, backend.Recorder, fixture.Select("SELECT id,i,b FROM t ORDER BY id", []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(2), int64(-7), int64(-7)}, {int64(3), nil, nil}}))
				}
			})
		}
	}
}
