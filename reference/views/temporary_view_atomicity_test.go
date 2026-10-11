package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTemporaryViewAtomicSuffix(t *testing.T) {
	temporaryViewScenario(t, func(s *fixture.TempReferenceSession, n *[]fixture.Diagnostic) {
		t := s.T
		s.Utility("CREATE TABLE suffix_sentinel(v INT)", nil)
		s.Exec(s.C, "INSERT INTO suffix_sentinel VALUES(7)", 1)
		q := "CREATE TEMP VIEW suffix_created AS SELECT 11 AS v; SELECT FROM"
		diagnostic := fixture.Diagnostic{Code: "42601", Message: "syntax error at end of input", Position: fixture.Position(len([]rune(q)) + 1)}
		step := fixture.ImplicitError(q, diagnostic)
		step.Prepared = s.Prepared
		step.ErrorStage = map[bool]string{false: "execute", true: "prepare"}[s.Prepared]
		fixture.RunStep(t, s.P.Ctx, s.C, s.Record, step)
		temporaryViewNotices(t, n)
		temporaryViewSelect(s, s.C, "SELECT viewname FROM pg_catalog.pg_views WHERE viewname='suffix_created'", []string{"viewname"}, []string{"NAME"}, [][]any{})
		temporaryViewSelect(s, s.C, "SELECT relname FROM pg_catalog.pg_class WHERE relname='suffix_created'", []string{"relname"}, []string{"NAME"}, [][]any{})
		fixture.TemporaryTableRows(s, s.C, "suffix_sentinel", 7)
		s.Exec(s.C, "BEGIN READ ONLY", 0)
		// Read-only CREATE fails before even the absent source lookup.
		s.Utility("CREATE TEMP VIEW readonly_failed AS SELECT v FROM readonly_absent", fixture.TempReferenceDiagnostic("25006", "cannot execute CREATE VIEW in a read-only transaction", "", ""))
		s.Exec(s.C, "ROLLBACK", 0)
		temporaryViewSelect(s, s.C, "SELECT viewname FROM pg_catalog.pg_views WHERE viewname='readonly_failed'", []string{"viewname"}, []string{"NAME"}, [][]any{})
		fixture.TemporaryTableRows(s, s.C, "suffix_sentinel", 7)
	})
}
