package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// A supported single-command rule replacement in WITH keeps both the original
// caller obligation and the replacement command's event-owner authority.
func TestReferenceRowSecurityRuleCTEActions(t *testing.T) {
	rlsBaseCases(t, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
		s.Exec("ALTER TABLE t ALTER COLUMN v DROP DEFAULT")
		fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "rls_owner", func() {
			s.Exec("CREATE TABLE rule_cte_event(id INT,v INT)")
		})
		s.Exec("CREATE RULE route AS ON INSERT TO rule_cte_event DO INSTEAD INSERT INTO t VALUES(NEW.id,NEW.v) RETURNING id,v")
		s.Exec("GRANT INSERT,SELECT ON rule_cte_event TO rls_reader")
		s.Exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		s.Actor("rls_reader")
		s.Exec("SET row_security=off")
		const q = "WITH moved AS (INSERT INTO rule_cte_event VALUES(3,30) RETURNING id,v) SELECT id,v FROM moved"
		predefinedSelect(s, q, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(3), int64(30)}})
		s.Actor("rls_owner")
		s.Run(fixture.Exec("DELETE FROM t WHERE id=3", 1))
		s.Actor("rls_reader")
		s.Exec("SET row_security=on")
		held := predefinedHeld(s, q)
		s.Actor("rls_owner")
		s.Exec("ALTER TABLE t FORCE ROW LEVEL SECURITY")
		s.Actor("rls_reader")
		s.Exec("SET row_security=off")
		check := func() {
			rowSecurityOff(s, q, "t", true, true)
			step := fixture.ErrorDiagnostic(q, rowSecurityOffDiagnostic("t", true))
			step.SelectRows, step.ErrorStage = true, "execute"
			fixture.RunStep(s.T, s.P.Ctx, held, s.Record, step)
		}
		check()
		s.Actor("rls_owner")
		s.Exec("REVOKE INSERT ON rule_cte_event FROM rls_reader")
		s.Actor("rls_reader")
		check()
		s.Exec("SET row_security=on")
		step := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table rule_cte_event"})
		step.SelectRows, step.ErrorStage = true, "execute"
		fixture.RunStep(s.T, s.P.Ctx, held, s.Record, step)
		rowSecurityUnchanged(s)
		s.Actor("rls_owner")
		predefinedSelect(s, "SELECT id,v FROM rule_cte_event", []string{"id", "v"}, []string{"INT4", "INT4"}, nil)
	})
}
