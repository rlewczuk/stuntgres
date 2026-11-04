package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func hbaSQLError(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, query string, diagnostic fixture.Diagnostic, prepared bool, stage string) {
	t.Helper()
	s := fixture.ImplicitError(query, diagnostic)
	s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, stage
	fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
}

func TestReferencePGHBAFileRulesDiagnostics(t *testing.T) {
	p, f := fixture.NewHBAFileScenario(t, hbaLocalSet("local all all trust\n", "trust"))
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE hba_diagnostic_sentinel(n int)", 0))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO hba_diagnostic_sentinel VALUES(42)", 1))
			for _, tc := range []struct{ query, code, message, position string }{
				{"SELECT missing FROM pg_catalog.pg_hba_file_rules", "42703", `column "missing" does not exist`, "8"},
				{"SELECT * FROM pg_catalog.no_hba_rules", "42P01", `relation "pg_catalog.no_hba_rules" does not exist`, "15"},
			} {
				for _, prepared := range []bool{false, true} {
					stage := "execute"
					if prepared {
						stage = "prepare"
					}
					hbaSQLError(t, p, b, b.DB, tc.query, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position}, prepared, stage)
				}
			}
			for _, prepared := range []bool{false, true} {
				hbaRead(t, p, b, b.DB, hbaSelect, [][]any{hbaLocal(1, f.Path(b, ""), 1, "trust")}, prepared)
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT n FROM hba_diagnostic_sentinel", []string{"n"}, []string{"INT4"}, [][]any{{int64(42)}}))
		})
	}
}
