package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceQuerySubqueryAdmission(t *testing.T) {
	// Native SQL ACL mutation remains outside the existing dialect; root-resumed
	// internal authorization units must exercise its real ACL setup seam.
	roleReferenceCleanup(t, []string{"subquery_reader"})
	subqueryReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		run(referenceExec("CREATE TABLE visible(id INT)", 0))
		run(referenceExec("INSERT INTO visible VALUES(1)", 1))
		run(referenceExec("CREATE TABLE secret(k INT,v INT)", 0))
		run(referenceExec("INSERT INTO secret VALUES(1,7)", 1))
		// Missing nested leaves fail analysis even beneath an empty outer source.
		for _, tc := range []struct{ q, position string }{
			{"SELECT EXISTS(SELECT 1 FROM missing) FROM visible WHERE FALSE", "29"},
			{"SELECT (SELECT v FROM missing) FROM visible WHERE FALSE", "23"},
			{"SELECT * FROM (SELECT * FROM missing) d WHERE FALSE", "30"},
			{"UPDATE visible SET id=(SELECT v FROM missing) WHERE FALSE", "38"},
			{"DELETE FROM visible WHERE EXISTS(SELECT 1 FROM missing) AND FALSE", "48"},
			{"INSERT INTO visible SELECT (SELECT v FROM missing) WHERE FALSE", "43"},
		} {
			want := controlsReferenceDiagnostic("42P01", `relation "missing" does not exist`)
			want.Position = tc.position
			subqueryAnalysisFailure(run, tc.q, want)
		}
		if !b.pg {
			return
		}
		run(referenceExec("CREATE ROLE subquery_reader", 0))
		run(referenceExec("GRANT USAGE ON SCHEMA public TO subquery_reader", 0))
		run(referenceExec("GRANT SELECT ON visible TO subquery_reader", 0))
		run(referenceExec("SET SESSION AUTHORIZATION subquery_reader", 0))
		defer func() {
			if _, err := c.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Error(err)
			}
		}()
		want := controlsReferenceDiagnostic("42501", "permission denied for table secret")
		for _, q := range []string{
			"SELECT EXISTS(SELECT 1 FROM secret) FROM visible WHERE FALSE",
			"SELECT (SELECT v FROM secret WHERE FALSE) FROM visible WHERE FALSE",
			"SELECT 1 IN (SELECT v FROM secret WHERE FALSE)",
			"SELECT * FROM (SELECT v FROM secret WHERE FALSE) d",
		} {
			s := referenceErrorDiagnostic(q, want)
			s.selectRows = true
			s.errorStage = "execute"
			run(s)
			s.prepared = true // Prepare validates names/types; authorization is execute-time.
			run(s)
		}
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("GRANT SELECT(k) ON secret TO subquery_reader", 0))
		run(referenceExec("SET SESSION AUTHORIZATION subquery_reader", 0))
		subqueryBoth(run, referenceSelect("SELECT EXISTS(SELECT 1 FROM secret WHERE k=1) AS allowed", []string{"allowed"}, []string{"BOOL"}, [][]any{{true}}))
		s := referenceErrorDiagnostic("SELECT EXISTS(SELECT v FROM secret WHERE k=1)", want)
		s.selectRows = true
		subqueryBoth(run, s) // Unused EXISTS projection is still an authorized read.
	})
}
