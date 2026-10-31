package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGRewriteQualNamespaceOwnership(t *testing.T) {
	for _, tc := range []struct {
		name, event, condition string
	}{
		{"old_insert", "INSERT", "EXISTS(SELECT 1 FROM rw_local_gate old WHERE old.v>0)"},
		{"new_delete", "DELETE", "EXISTS(SELECT 1 FROM rw_local_gate new WHERE new.v>0)"},
		{"whole_old_insert", "INSERT", "EXISTS(SELECT 1 FROM rw_local_gate old WHERE old.* IS NOT NULL)"},
		{"whole_new_delete", "DELETE", "EXISTS(SELECT 1 FROM rw_local_gate new WHERE new.* IS NOT NULL)"},
		{"outer_local_alias", "INSERT", "EXISTS(SELECT 1 FROM rw_local_gate old WHERE EXISTS(SELECT 1 WHERE old.v>0))"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					rewriteExec(t, p, b, "CREATE TABLE rw_local_event(v integer)", "CREATE TABLE rw_local_gate(v integer)", "CREATE TABLE rw_local_log(v integer)")
					rewriteEffect(t, p, b, "INSERT INTO rw_local_gate VALUES(NULL)", 1)
					if tc.event == "DELETE" {
						rewriteEffect(t, p, b, "INSERT INTO rw_local_event VALUES(1)", 1)
					}
					row := "NEW"
					if tc.event == "DELETE" {
						row = "OLD"
					}
					// The named child source is local even when its alias spells a
					// pseudo row forbidden for this event. Permission follows the
					// actual bound owner, not the alias's spelling.
					rewriteExec(t, p, b, "CREATE RULE r AS ON "+tc.event+" TO rw_local_event WHERE "+tc.condition+" DO ALSO INSERT INTO rw_local_log VALUES("+row+".v)")
					before := rewriteTrees(t, p, b, "rw_local_event", "r")
					inquiry := func() {
						t.Helper()
						for _, suffix := range []string{"", ",false", ",true"} {
							rewriteRows(t, p, b, fixture.Select("SELECT pg_get_expr(ev_qual,0"+suffix+") IS NOT NULL AS local_expression FROM pg_rewrite WHERE ev_class='rw_local_event'::regclass AND rulename='r'", []string{"local_expression"}, []string{"BOOL"}, [][]any{{true}}))
						}
					}
					inquiry()
					first, second := "INSERT INTO rw_local_event VALUES(1)", "INSERT INTO rw_local_event VALUES(2)"
					if tc.event == "DELETE" {
						first, second = "DELETE FROM rw_local_event WHERE v=1", "DELETE FROM rw_local_event WHERE v=2"
					}
					// Local NULL is a nonmatch; the event's positive value must not
					// accidentally supply the condition's scalar or whole-row member.
					rewriteEffect(t, p, b, first, 1)
					rewriteCount(t, p, b, "SELECT count(*) FROM rw_local_log", 0)
					rewriteEffect(t, p, b, "UPDATE rw_local_gate SET v=7", 1)
					if tc.event == "DELETE" {
						rewriteEffect(t, p, b, "INSERT INTO rw_local_event VALUES(2)", 1)
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(fixture.Exec(second, 1)))
					rewriteRows(t, p, b, fixture.Select("SELECT v FROM rw_local_log", []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}}))
					rewriteExec(t, p, b, "ALTER TABLE rw_local_gate RENAME COLUMN v TO amount")
					if got := rewriteTrees(t, p, b, "rw_local_event", "r"); got != before {
						t.Fatal("local source rename mutated captured rule trees", got, before)
					}
					inquiry()
				})
			}
		})
	}
}
