package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGRewriteWholeRow(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			rewriteExec(t, p, b, "CREATE TABLE rw_whole_event(v integer)", "CREATE TABLE rw_whole_log(v integer)",
				"CREATE RULE old_row AS ON UPDATE TO rw_whole_event WHERE OLD.* IS DISTINCT FROM NULL DO ALSO NOTHING",
				"CREATE RULE new_row AS ON UPDATE TO rw_whole_event WHERE NEW.* IS DISTINCT FROM NULL DO ALSO NOTHING",
				"CREATE RULE logged AS ON UPDATE TO rw_whole_event WHERE OLD.* IS DISTINCT FROM NULL DO ALSO INSERT INTO rw_whole_log VALUES(NEW.v)")
			old := rewriteTrees(t, p, b, "rw_whole_event", "old_row")
			newTree := rewriteTrees(t, p, b, "rw_whole_event", "new_row")
			if old[0] == newTree[0] {
				t.Fatal("actual OLD and NEW whole-row qualifications collapsed")
			}
			for _, name := range []string{"old_row", "new_row"} {
				for _, oid := range []string{"0", "'rw_whole_event'::regclass", "4294967295::oid"} {
					for _, suffix := range []string{"", ",false", ",true"} {
						query := "SELECT pg_get_expr(ev_qual," + oid + suffix + ") AS expression FROM pg_rewrite WHERE ev_class='rw_whole_event'::regclass AND rulename='" + name + "'"
						message := ""
						if oid == "0" {
							message = "expression contains variables"
						} else if name == "new_row" {
							message = "expression contains variables of more than one relation"
						}
						if message != "" {
							for _, prepared := range []bool{false, true} {
								step := fixture.ImplicitError(query, fixture.Diagnostic{Code: "22023", Message: message})
								step.SelectRows, step.Prepared, step.ErrorStage = true, prepared, "execute"
								fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step)
							}
						} else {
							var want any = "(rw_whole_event.* IS DISTINCT FROM NULL)"
							if suffix == ",true" {
								want = "rw_whole_event.* IS DISTINCT FROM NULL"
							}
							if oid == "4294967295::oid" {
								want = nil
							}
							rewriteRows(t, p, b, fixture.Select(query, []string{"expression"}, []string{"TEXT"}, [][]any{{want}}))
						}
					}
				}
			}
			// A whole row is a non-NULL datum even when every member is NULL.
			rewriteEffect(t, p, b, "INSERT INTO rw_whole_event VALUES(NULL)", 1)
			rewriteEffect(t, p, b, "UPDATE rw_whole_event SET v=7", 1)
			rewriteEffect(t, p, b, "UPDATE rw_whole_event SET v=NULL", 1)
			rewriteRows(t, p, b, fixture.Select("SELECT v FROM rw_whole_log ORDER BY v NULLS LAST", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}, {nil}}))
			rewriteExec(t, p, b, "ALTER TABLE rw_whole_event RENAME TO rw_whole_renamed")
			if got := rewriteTrees(t, p, b, "rw_whole_renamed", "old_row"); got != old {
				t.Fatal("rename mutated whole-row predecessor", got, old)
			}
			rewriteRows(t, p, b, fixture.Select("SELECT pg_get_expr(ev_qual,ev_class) AS expression FROM pg_rewrite WHERE ev_class='rw_whole_renamed'::regclass AND rulename='old_row'", []string{"expression"}, []string{"TEXT"}, [][]any{{"(rw_whole_renamed.* IS DISTINCT FROM NULL)"}}))
		})
	}
}
