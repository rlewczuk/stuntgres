package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

const rowSecurityOwnerHint = "To disable the policy for the table's owner, use ALTER TABLE NO FORCE ROW LEVEL SECURITY."

func rowSecurityOffDiagnostic(table string, owner bool) fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic("42501", `query would be affected by row-level security policy for table "`+table+`"`)
	if owner {
		d.Hint = rowSecurityOwnerHint
	}
	return d
}

// PostgreSQL's rewrite guard rejects fresh Prepare, not just execution.
// Full optional diagnostic fields are intentionally empty, not ignored.
func rowSecurityOff(s *membershipScenario, q, table string, query, owner bool) {
	s.t.Helper()
	predefinedFailure(s, q, rowSecurityOffDiagnostic(table, owner), query, "prepare")
}

func rowSecurityValue(s *membershipScenario, value string) {
	s.t.Helper()
	for _, q := range []string{"SHOW row_security", "SELECT current_setting('row_security')"} {
		column := "row_security"
		if q[0] == 'S' && q != "SHOW row_security" {
			column = "current_setting"
		}
		s.run(fixture.Select(q, []string{column}, []string{"TEXT"}, [][]any{{value}}))
	}
}

func rowSecurityUnchanged(s *membershipScenario) {
	s.t.Helper()
	s.actor("rls_bypass")
	predefinedSelect(s, "SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}})
}

func rowSecurityPeer(s *membershipScenario, db *sql.DB) *membershipScenario {
	s.t.Helper()
	c, err := db.Conn(s.p.Ctx)
	if err != nil {
		s.t.Fatal(err)
	}
	s.t.Cleanup(func() { c.Close() })
	peer := *s
	peer.c = c
	peer.actor("rls_reader")
	return &peer
}

func rowSecurityRecovery(s *membershipScenario) {
	s.t.Helper()
	predefinedFailure(s, "SELECT id FROM t", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}, true, "prepare")
	s.exec("ROLLBACK TO SAVEPOINT healthy")
}

func rowSecurityHeldOff(s *membershipScenario, held fixture.HeldStatement, query bool) {
	s.t.Helper()
	step := fixture.ErrorDiagnostic(held.SQL, rowSecurityOffDiagnostic("t", false))
	step.SelectRows, step.ErrorStage = query, "execute"
	fixture.RunStep(s.t, s.p.Ctx, held, s.record, step)
}

func rowSecurityHeldRows(s *membershipScenario, held fixture.HeldStatement, rows [][]any) {
	s.t.Helper()
	fixture.RunStep(s.t, s.p.Ctx, held, s.record, fixture.Select(held.SQL, []string{"id"}, []string{"INT4"}, rows))
}

func rowSecurityCase(s *membershipScenario, name string, body func()) {
	s.t.Helper()
	s.t.Run(name, func(t *testing.T) {
		previous := s.t
		s.t = t
		defer func() { s.t = previous }()
		body()
	})
}
