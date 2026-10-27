package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRowSecurityTransactions(t *testing.T) {
	rlsBaseCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		s.Exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		s.Actor("rls_reader")
		defer s.C.ExecContext(s.P.Ctx, "ROLLBACK")
		peer := rowSecurityPeer(s, db)
		rowSecurityValue(s, "on")
		rowSecurityValue(peer, "on")
		s.Exec("SET row_security=off")
		rowSecurityMetadata(s, "off", "session")
		rowSecurityMetadata(peer, "on", "default")
		rlsBaseRows(peer, nil)
		s.Exec("RESET row_security")
		rowSecurityCase(s, "session_rollback", func() {
			s.Exec("BEGIN")
			s.Exec("SET row_security=off")
			rowSecurityMetadata(s, "off", "session")
			s.Exec("ROLLBACK")
			rowSecurityMetadata(s, "on", "default")
			rlsBaseRows(s, nil)
		})
		for _, local := range []string{"SESSION", "LOCAL"} {
			rowSecurityCase(s, "savepoint_"+local, func() {
				s.Exec("BEGIN")
				s.Exec("SAVEPOINT healthy")
				s.Exec("SET " + local + " row_security=off")
				rowSecurityMetadata(s, "off", "session")
				rowSecurityOff(s, "SELECT id FROM t WHERE false", "t", true, false)
				rowSecurityRecovery(s)
				rowSecurityMetadata(s, "on", "default")
				rlsBaseRows(s, nil)
				s.Exec("COMMIT")
				rowSecurityMetadata(s, "on", "default")
			})
		}
		rowSecurityCase(s, "local_commit", func() {
			s.Exec("BEGIN")
			predefinedSelect(s, "SELECT set_config('row_security','off',true)", []string{"set_config"}, []string{"TEXT"}, [][]any{{"off"}})
			rowSecurityMetadata(s, "off", "session")
			s.Exec("COMMIT")
			rowSecurityMetadata(s, "on", "default")
			rlsBaseRows(s, nil)
		})
		for _, finish := range []string{"COMMIT", "ROLLBACK"} {
			rowSecurityCase(s, "session_then_local_"+finish, func() {
				s.Exec("RESET row_security")
				s.Exec("BEGIN")
				s.Exec("SET SESSION row_security=off")
				s.Exec("SET LOCAL row_security=on")
				rowSecurityMetadata(s, "on", "session")
				rlsBaseRows(s, nil)
				s.Exec(finish)
				if finish == "COMMIT" {
					rowSecurityMetadata(s, "off", "session")
					rowSecurityOff(s, "SELECT id FROM t", "t", true, false)
				} else {
					rowSecurityMetadata(s, "on", "default")
					rlsBaseRows(s, nil)
				}
			})
		}
		rowSecurityCase(s, "invalid_aborts_and_recovers", func() {
			s.Exec("RESET row_security")
			s.Exec("BEGIN")
			s.Exec("SAVEPOINT healthy")
			predefinedFailure(s, "SET row_security='bogus'", fixture.Diagnostic{Code: "22023", Message: `parameter "row_security" requires a Boolean value`}, false, "execute")
			rowSecurityRecovery(s)
			rowSecurityMetadata(s, "on", "default")
			rlsBaseRows(s, nil)
			s.Exec("COMMIT")
		})
		rowSecurityMetadata(peer, "on", "default")
		rlsBaseRows(peer, nil)
		rowSecurityUnchanged(s)
	})
}
