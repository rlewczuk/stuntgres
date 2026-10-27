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
func rowSecurityOff(s *fixture.RoleMembershipScenario, q, table string, query, owner bool) {
	s.T.Helper()
	predefinedFailure(s, q, rowSecurityOffDiagnostic(table, owner), query, "prepare")
}

func rowSecurityValue(s *fixture.RoleMembershipScenario, value string) {
	s.T.Helper()
	for _, q := range []string{"SHOW row_security", "SELECT current_setting('row_security')"} {
		column := "row_security"
		if q[0] == 'S' && q != "SHOW row_security" {
			column = "current_setting"
		}
		s.Run(fixture.Select(q, []string{column}, []string{"TEXT"}, [][]any{{value}}))
	}
}

func rowSecurityUnchanged(s *fixture.RoleMembershipScenario) {
	s.T.Helper()
	s.Actor("rls_bypass")
	predefinedSelect(s, "SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}})
}

func rowSecurityPeer(s *fixture.RoleMembershipScenario, db *sql.DB) *fixture.RoleMembershipScenario {
	s.T.Helper()
	c, err := db.Conn(s.P.Ctx)
	if err != nil {
		s.T.Fatal(err)
	}
	s.T.Cleanup(func() { c.Close() })
	peer := *s
	peer.C = c
	peer.Actor("rls_reader")
	return &peer
}

func rowSecurityRecovery(s *fixture.RoleMembershipScenario) {
	s.T.Helper()
	predefinedFailure(s, "SELECT id FROM t", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}, true, "prepare")
	s.Exec("ROLLBACK TO SAVEPOINT healthy")
}

func rowSecurityHeldOff(s *fixture.RoleMembershipScenario, held fixture.HeldStatement, query bool) {
	s.T.Helper()
	step := fixture.ErrorDiagnostic(held.SQL, rowSecurityOffDiagnostic("t", false))
	step.SelectRows, step.ErrorStage = query, "execute"
	fixture.RunStep(s.T, s.P.Ctx, held, s.Record, step)
}

func rowSecurityHeldRows(s *fixture.RoleMembershipScenario, held fixture.HeldStatement, rows [][]any) {
	s.T.Helper()
	fixture.RunStep(s.T, s.P.Ctx, held, s.Record, fixture.Select(held.SQL, []string{"id"}, []string{"INT4"}, rows))
}

func rowSecurityCase(s *fixture.RoleMembershipScenario, name string, body func()) {
	s.T.Helper()
	s.T.Run(name, func(t *testing.T) {
		previous := s.T
		s.T = t
		defer func() { s.T = previous }()
		body()
	})
}
