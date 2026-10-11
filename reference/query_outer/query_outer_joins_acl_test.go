package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryOuterJoinsACL(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"outer_join_reader"})
	fixture.OuterJoinsBackendScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, backend fixture.Backend) {
		for _, kind := range []string{"LEFT", "RIGHT", "FULL"} {
			fixture.OuterJoinsCases(t, p, conn, backend.Recorder, fixture.Select("SELECT 1 FROM a "+kind+" JOIN b USING(k) LIMIT 0", []string{"?column?"}, []string{"INT4"}, nil))
		}
		if backend.PG {
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("GRANT USAGE ON SCHEMA public TO PUBLIC", 0))
		}
		for _, step := range []fixture.Step{fixture.Exec("CREATE ROLE outer_join_reader", 0), fixture.Exec("GRANT SELECT(k) ON a,b,empty_a,empty_b TO outer_join_reader", 0), fixture.Exec("SET SESSION AUTHORIZATION outer_join_reader", 0)} {
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, step)
		}
		defer conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		for _, kind := range []string{"LEFT", "RIGHT", "FULL"} {
			fixture.OuterJoinsCases(t, p, conn, backend.Recorder, fixture.Select("SELECT 1 FROM a "+kind+" JOIN b USING(k) LIMIT 0", []string{"?column?"}, []string{"INT4"}, nil))
		}
		if _, err := backend.DB.ExecContext(p.Ctx, "REVOKE SELECT(k) ON b,empty_b FROM outer_join_reader"); err != nil {
			t.Fatal(err)
		}
		for _, kind := range []string{"LEFT", "RIGHT", "FULL"} {
			for _, table := range []string{"b", "empty_b"} {
				for _, condition := range []string{" USING(k)", " ON FALSE"} {
					for _, suffix := range []string{"", " LIMIT 0"} {
						query := "SELECT 1 FROM a " + kind + " JOIN " + table + condition + suffix
						step := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for table " + table})
						step.SelectRows, step.ErrorStage = true, "execute"
						fixture.RunStep(t, p.Ctx, conn, backend.Recorder, step)
						fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Prepared(step))
					}
				}
			}
		}
	})
}
