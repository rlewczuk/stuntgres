package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// PostgreSQL can create the precommit view closure; native deliberately retains
// its CREATE VIEW explicit-transaction boundary instead of simulating that path.
func TestReferenceTemporaryViewTransactionBoundary(t *testing.T) {
	temporaryViewScenario(t, func(s *fixture.TempReferenceSession, n *[]fixture.Diagnostic) {
		t := s.T
		s.Utility("CREATE TABLE transaction_sentinel(v INT)", nil)
		s.Exec(s.C, "INSERT INTO transaction_sentinel VALUES(7)", 1)
		s.Exec(s.C, "BEGIN", 0)
		s.Utility("CREATE TEMP TABLE transaction_base(v INT) ON COMMIT DROP", nil)
		s.Exec(s.C, "INSERT INTO transaction_base VALUES(11)", 1)
		if s.Postgres {
			s.Utility("CREATE TEMP VIEW transaction_v AS SELECT v FROM transaction_base", nil)
			s.Utility("CREATE VIEW transaction_w AS SELECT v FROM transaction_v", nil)
			temporaryViewNotices(t, n, "transaction_w")
			fixture.TemporaryTableRows(s, s.C, "transaction_w", 11)
			s.Exec(s.C, "COMMIT", 0)
		} else {
			d := fixture.TempReferenceDiagnostic("0A000", "statement is unsupported in an explicit transaction", "", "")
			s.Utility("CREATE TEMP VIEW transaction_v AS SELECT v FROM transaction_base", d)
			temporaryViewNotices(t, n)
			s.Exec(s.C, "ROLLBACK", 0)
		}
		s.Missing(s.C, "transaction_base", "v", "15")
		s.Missing(s.C, "transaction_v", "v", "15")
		s.Missing(s.C, "transaction_w", "v", "15")
		fixture.TemporaryTableRows(s, s.C, "transaction_sentinel", 7)
		temporaryViewSelect(s, s.C, "SELECT viewname FROM pg_catalog.pg_views WHERE viewname='transaction_v' OR viewname='transaction_w'", []string{"viewname"}, []string{"NAME"}, [][]any{})
	})
}
