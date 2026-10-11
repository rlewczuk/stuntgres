package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func referenceMathPrivileges(t *testing.T) {
	const reader = "math_column_reader"
	fixture.RoleReferenceCleanup(t, []string{reader})
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		conn, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		defer conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
		if backend.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO PUBLIC", 0))
		}
		for _, q := range []string{"CREATE ROLE " + reader, "CREATE TABLE m(n NUMERIC,unused NUMERIC)", "GRANT SELECT(unused) ON m TO " + reader} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO m VALUES(1.25,9)", 1))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+reader, 0))
		for _, q := range []string{"SELECT round(trim_scale(n),1) FROM m", "SELECT round(n,1),count(*) FROM m GROUP BY 1"} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table m"})
				s.SelectRows = true
				s.ErrorStage = "execute"
				s.Prepared = prepared
				run(s)
			}
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT(n) ON m TO "+reader, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+reader, 0))
		s := fixture.Select("SELECT round(trim_scale(n),1) FROM m", []string{"round"}, []string{"NUMERIC"}, [][]any{{"1.3"}})
		run(s)
		run(fixture.Prepared(s))
	}
}
