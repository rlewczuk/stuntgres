package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsAnalyzedUtilityIdentity(t *testing.T) {
	pgssCase(t, true, func(s *pgssScenario) {
		s.exec("CREATE TABLE pgss_part.pgss_utility_identity_source(k integer)")
		s.run(fixture.Exec("INSERT INTO pgss_part.pgss_utility_identity_source VALUES(1)", 1))
		s.work(fixture.Exec("SET pg_stat_statements.track_utility=on", 0))
		ctas := "CREATE TABLE pgss_part.pgss_utility_identity_target AS SELECT a.k+%d AS v FROM pgss_part.pgss_utility_identity_source a"
		explain := "EXPLAIN SELECT b.k+%d AS pgss_explain_identity FROM pgss_part.pgss_utility_identity_source b"
		declare := "DECLARE pgss_identity_cursor CURSOR FOR SELECT c.k+%d AS pgss_declare_identity FROM pgss_part.pgss_utility_identity_source c"
		s.counts("CREATE TABLE pgss_part.pgss_utility_identity_target AS%", 0, 0, 0)
		s.counts("EXPLAIN SELECT b.k+% AS pgss_explain_identity%", 0, 0, 0)
		s.counts("DECLARE pgss_identity_cursor CURSOR FOR%", 0, 0, 0)
		for i := 1; i <= 2; i++ {
			work := func(step fixture.Step) {
				if i == 2 {
					step = fixture.Prepared(step)
				}
				s.work(step)
			}
			work(fixture.Exec(fmt.Sprintf(ctas, i), 1))
			s.both(fixture.Select("SELECT v FROM pgss_part.pgss_utility_identity_target", []string{"v"}, []string{"INT4"}, [][]any{{int64(1 + i)}}))
			s.exec("DROP TABLE pgss_part.pgss_utility_identity_target")
			// Existing EXPLAIN scenarios own plan-output semantics. Here the
			// public Exec path consumes it while testing the utility identity.
			work(fixture.Exec(fmt.Sprintf(explain, i), 0))
			s.work(fixture.Exec("BEGIN", 0))
			defer s.worker.ExecContext(s.p.Ctx, "ROLLBACK")
			work(fixture.Exec(fmt.Sprintf(declare, i), 0))
			s.work(fixture.Select("FETCH NEXT FROM pgss_identity_cursor", []string{"pgss_declare_identity"}, []string{"INT4"}, [][]any{{int64(1 + i)}}))
			s.work(fixture.Exec("CLOSE pgss_identity_cursor", 0))
			s.work(fixture.Exec("COMMIT", 0))
		}
		// These utility children are analyzed Query nodes in PostgreSQL,
		// unlike CREATE VIEW's raw query syntax: changed literals merge.
		s.entry("CREATE TABLE pgss_part.pgss_utility_identity_target AS SELECT a.k+$1 AS v FROM pgss_part.pgss_utility_identity_source a", 2, 2)
		s.entry("EXPLAIN SELECT b.k+$1 AS pgss_explain_identity FROM pgss_part.pgss_utility_identity_source b", 2, 0)
		s.entry("DECLARE pgss_identity_cursor CURSOR FOR SELECT c.k+$1 AS pgss_declare_identity FROM pgss_part.pgss_utility_identity_source c", 2, 0)
	})
}
