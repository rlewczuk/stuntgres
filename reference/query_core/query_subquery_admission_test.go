package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQuerySubqueryAdmission(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"subquery_reader"})
	subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		run(fixture.Exec("CREATE TABLE visible(id INT)", 0))
		run(fixture.Exec("INSERT INTO visible VALUES(1)", 1))
		run(fixture.Exec("CREATE TABLE secret(k INT,v INT)", 0))
		run(fixture.Exec("INSERT INTO secret VALUES(1,7)", 1))
		// Missing nested leaves fail analysis even beneath an empty outer source.
		for _, tc := range []struct{ q, position string }{
			{"SELECT EXISTS(SELECT 1 FROM missing) FROM visible WHERE FALSE", "29"},
			{"SELECT (SELECT v FROM missing) FROM visible WHERE FALSE", "23"},
			{"SELECT * FROM (SELECT * FROM missing) d WHERE FALSE", "30"},
			{"UPDATE visible SET id=(SELECT v FROM missing) WHERE FALSE", "38"},
			{"DELETE FROM visible WHERE EXISTS(SELECT 1 FROM missing) AND FALSE", "48"},
			{"INSERT INTO visible SELECT (SELECT v FROM missing) WHERE FALSE", "43"},
		} {
			want := fixture.ControlsReferenceDiagnostic("42P01", `relation "missing" does not exist`)
			want.Position = tc.position
			subqueryAnalysisFailure(run, tc.q, want)
		}
		run(fixture.Exec("CREATE ROLE subquery_reader", 0))
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO subquery_reader", 0))
		}
		run(fixture.Exec("GRANT SELECT ON visible TO subquery_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION subquery_reader", 0))
		defer func() {
			if _, err := c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Error(err)
			}
		}()
		want := fixture.ControlsReferenceDiagnostic("42501", "permission denied for table secret")
		for _, q := range []string{
			"SELECT EXISTS(SELECT 1 FROM secret) FROM visible WHERE FALSE",
			"SELECT (SELECT v FROM secret WHERE FALSE) FROM visible WHERE FALSE",
			"SELECT 1 IN (SELECT v FROM secret WHERE FALSE)",
			"SELECT * FROM (SELECT v FROM secret WHERE FALSE) d",
		} {
			s := fixture.ErrorDiagnostic(q, want)
			s.SelectRows = true
			s.ErrorStage = "execute"
			run(s)
			s.Prepared = true // Prepare validates names/types; authorization is execute-time.
			run(s)
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT(k) ON secret TO subquery_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION subquery_reader", 0))
		subqueryBoth(run, fixture.Select("SELECT EXISTS(SELECT 1 FROM secret WHERE k=1) AS allowed", []string{"allowed"}, []string{"BOOL"}, [][]any{{true}}))
		s := fixture.ErrorDiagnostic("SELECT EXISTS(SELECT v FROM secret WHERE k=1)", want)
		s.SelectRows = true
		subqueryBoth(run, s) // Unused EXISTS projection is still an authorized read.
	})
}
