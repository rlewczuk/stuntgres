package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func holdCommitWarningPolicy(t *testing.T) {
	for _, policy := range []string{"warning", "error"} {
		t.Run(policy, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					var notices []fixture.Diagnostic
					_, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
					defer c.ExecContext(context.Background(), "ROLLBACK")
					run := func(s fixture.Step) { t.Helper(); fixture.RegtypeRunStep(t, p.Ctx, c, recorder, s) }
					run(fixture.Exec("CREATE TABLE hold_regtype_rows(t TEXT)", 0))
					run(fixture.Exec("INSERT INTO hold_regtype_rows VALUES('timestamp(7)'),('timestamp(7)')", 2))
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("SET LOCAL client_min_messages TO error", 0))
					q := "DECLARE warning_h NO SCROLL CURSOR WITH HOLD FOR SELECT t::REGTYPE AS r FROM hold_regtype_rows"
					notices = nil
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					fixture.RegtypeAssertNotices(t, notices, nil)
					run(fixture.Exec(q, 0))
					stmt.Close()
					fixture.RegtypeAssertNotices(t, notices, nil)
					run(fixture.Exec("SET LOCAL client_min_messages TO "+policy, 0))
					notices = nil
					run(fixture.Exec("COMMIT", 0))
					warning := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "TIMESTAMP(7) precision reduced to maximum allowed, 6"}
					var want []fixture.Diagnostic
					if policy == "warning" {
						want = []fixture.Diagnostic{warning, warning}
					}
					fixture.RegtypeAssertNotices(t, notices, want)
					notices = nil
					run(fixture.Exec("SET client_min_messages TO warning", 0))
					run(fixture.Select("FETCH ALL FROM warning_h", []string{"r"}, []string{"REGTYPE"}, [][]any{{"timestamp without time zone"}, {"timestamp without time zone"}}))
					fixture.RegtypeAssertNotices(t, notices, nil)
					run(fixture.Select("FETCH ALL FROM warning_h", []string{"r"}, []string{"REGTYPE"}, [][]any{}))
					fixture.RegtypeAssertNotices(t, notices, nil)
				})
			}
		})
	}
}
