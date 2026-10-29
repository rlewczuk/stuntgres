package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceVacuumProgressReadOnly(t *testing.T) {
	vacuumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := vacuumConn(t, p, b)
		vacuumRun(t, p, b, c, fixture.Exec("CREATE TABLE vacuum_progress_sentinel(id INT)", 0))
		vacuumRun(t, p, b, c, fixture.Exec("INSERT INTO vacuum_progress_sentinel VALUES(7)", 1))
		for _, tc := range []struct{ name, query, verb, gerund, action string }{
			{"insert", "INSERT INTO pg_stat_progress_vacuum(pid) VALUES(1)", "insert into", "inserting into", "INSERT"},
			{"update", "UPDATE pg_stat_progress_vacuum SET pid=1 WHERE false", "update", "updating", "UPDATE"},
			{"delete", "DELETE FROM pg_stat_progress_vacuum WHERE false", "delete from", "deleting from", "DELETE"},
		} {
			for _, prepared := range []bool{false, true} {
				name := tc.name + "/direct"
				if prepared {
					name = tc.name + "/prepared"
				}
				t.Run(name, func(t *testing.T) {
					want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: "cannot " + tc.verb + " view \"pg_stat_progress_vacuum\"", Detail: "Views that do not select from a single table or view are not automatically updatable.", Hint: "To enable " + tc.gerund + " the view, provide an INSTEAD OF " + tc.action + " trigger or an unconditional ON " + tc.action + " DO INSTEAD rule."}
					s := fixture.ImplicitError(tc.query, want)
					s.Prepared = prepared
					s.ErrorStage = "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					vacuumRun(t, p, b, c, s)
					vacuumProgressEmpty(t, p, b, c)
					vacuumRows(t, p, b, c, "vacuum_progress_sentinel", 7)
				})
			}
		}
	})
}
