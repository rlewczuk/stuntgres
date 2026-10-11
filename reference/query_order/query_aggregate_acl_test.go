package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCountExpressionPostgresColumnACL(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"aggregate_reader"})
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step) }
		run(fixture.Exec("CREATE ROLE aggregate_reader", 0))
		run(fixture.Exec("CREATE TABLE t(k TEXT,v INT)", 0))
		if backend.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO aggregate_reader", 0))
		}
		conn, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		defer conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("SET SESSION AUTHORIZATION aggregate_reader", 0))
		denied := fixture.ImplicitError("SELECT count(*) FROM t", fixture.Diagnostic{Code: "42501", Message: "permission denied for table t"})
		denied.SelectRows = true
		fixture.RunStep(t, p.Ctx, conn, backend.Recorder, denied)
		if _, err := backend.DB.ExecContext(p.Ctx, "GRANT SELECT(v) ON t TO aggregate_reader"); err != nil {
			t.Fatal(err)
		}
		success := fixture.Select("SELECT count(*) FROM t", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}})
		fixture.RunStep(t, p.Ctx, conn, backend.Recorder, success)
		fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Prepared(success))
		expression := fixture.Select("SELECT count(v) FROM t", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}})
		fixture.RunStep(t, p.Ctx, conn, backend.Recorder, expression)
		fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Prepared(expression))
		for _, q := range []string{"SELECT count(k) FROM t", "SELECT count(k) FROM t WHERE FALSE", "SELECT count(v) FROM t HAVING count(k)>0", "SELECT count(v) FROM t ORDER BY count(k)", "SELECT count(*) FROM t GROUP BY k", "SELECT count(*) FROM t GROUP BY k HAVING k IS NULL", "SELECT count(*) FROM t GROUP BY k ORDER BY k"} {
			step := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table t"})
			step.SelectRows = true
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, step)
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Prepared(step))
		}
	}
}
