package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGRulesDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE pgr_diag(id INT)", 0))
			run(fixture.Exec("INSERT INTO pgr_diag VALUES(7)", 1))
			run(fixture.Exec("CREATE RULE r AS ON DELETE TO pgr_diag DO NOTHING", 0))
			for _, prepared := range []bool{false, true} {
				for _, column := range []string{"oid", "ev_type", "ev_qual", "ev_action"} {
					s := fixture.ImplicitError("SELECT "+column+" FROM pg_catalog.pg_rules", fixture.Diagnostic{Code: "42703", Message: "column \"" + column + "\" does not exist", Position: "8"})
					s.SelectRows = true
					if prepared {
						s = fixture.Prepared(s)
						s.ErrorStage = "prepare"
					} else {
						s.ErrorStage = "execute"
					}
					run(s)
				}
				for _, c := range []struct{ query, code, message, position string }{
					{"SELECT r.absent FROM pg_catalog.pg_rules r", "42703", "column r.absent does not exist", "8"},
					{"SELECT schemaname FROM pg_catalog.pg_rules a JOIN pg_catalog.pg_rules b ON a.rulename=b.rulename", "42702", `column reference "schemaname" is ambiguous`, "8"},
				} {
					s := fixture.ImplicitError(c.query, fixture.Diagnostic{Code: c.code, Message: c.message, Position: c.position})
					s.SelectRows = true
					if prepared {
						s = fixture.Prepared(s)
						s.ErrorStage = "prepare"
					} else {
						s.ErrorStage = "execute"
					}
					run(s)
				}
				for _, c := range []struct{ query, verb, command, hintAction string }{
					{"INSERT INTO pg_catalog.pg_rules VALUES('public','pgr_diag','fake','NOTHING')", "insert into", "INSERT", "inserting into"},
					{"UPDATE pg_catalog.pg_rules SET definition='changed' WHERE tablename='pgr_diag'", "update", "UPDATE", "updating"},
					{"DELETE FROM pg_catalog.pg_rules WHERE tablename='pgr_diag'", "delete from", "DELETE", "deleting from"},
				} {
					s := fixture.ImplicitError(c.query, fixture.Diagnostic{Code: "55000", Message: "cannot " + c.verb + " view \"pg_rules\"", Detail: "Views that do not select from a single table or view are not automatically updatable.", Hint: "To enable " + c.hintAction + " the view, provide an INSTEAD OF " + c.command + " trigger or an unconditional ON " + c.command + " DO INSTEAD rule."})
					if prepared {
						s = fixture.Prepared(s)
					}
					run(s)
					pgRulesCheck(t, p, b, b.DB, pgRulesProbe, [][]any{{"public", "pgr_diag", "r", "CREATE RULE r AS\n    ON DELETE TO public.pgr_diag DO NOTHING;"}}, "public", "pgr_diag", "r")
					run(fixture.Select("SELECT id FROM pgr_diag", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
				}
			}
		})
	}
}
