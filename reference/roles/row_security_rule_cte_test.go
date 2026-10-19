package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// A supported single-command rule replacement in WITH keeps both the original
// caller obligation and the replacement command's event-owner authority.
func TestReferenceRowSecurityRuleCTEActions(t *testing.T) {
	rlsBaseCases(t, func(s *membershipScenario, _ *sql.DB) {
		s.exec("ALTER TABLE t ALTER COLUMN v DROP DEFAULT")
		fixture.WithPublicSchemaOwner(s.t, s.p, s.backend, "rls_owner", func() {
			s.exec("CREATE TABLE rule_cte_event(id INT,v INT)")
		})
		s.exec("CREATE RULE route AS ON INSERT TO rule_cte_event DO INSTEAD INSERT INTO t VALUES(NEW.id,NEW.v) RETURNING id,v")
		s.exec("GRANT INSERT,SELECT ON rule_cte_event TO rls_reader")
		s.exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		s.actor("rls_reader")
		s.exec("SET row_security=off")
		const q = "WITH moved AS (INSERT INTO rule_cte_event VALUES(3,30) RETURNING id,v) SELECT id,v FROM moved"
		predefinedSelect(s, q, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(3), int64(30)}})
		s.actor("rls_owner")
		s.run(fixture.Exec("DELETE FROM t WHERE id=3", 1))
		s.actor("rls_reader")
		s.exec("SET row_security=on")
		held := predefinedHeld(s, q)
		s.actor("rls_owner")
		s.exec("ALTER TABLE t FORCE ROW LEVEL SECURITY")
		s.actor("rls_reader")
		s.exec("SET row_security=off")
		check := func() {
			rowSecurityOff(s, q, "t", true, true)
			step := fixture.ErrorDiagnostic(q, rowSecurityOffDiagnostic("t", true))
			step.SelectRows, step.ErrorStage = true, "execute"
			fixture.RunStep(s.t, s.p.Ctx, held, s.record, step)
		}
		check()
		s.actor("rls_owner")
		s.exec("REVOKE INSERT ON rule_cte_event FROM rls_reader")
		s.actor("rls_reader")
		check()
		s.exec("SET row_security=on")
		step := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table rule_cte_event"})
		step.SelectRows, step.ErrorStage = true, "execute"
		fixture.RunStep(s.t, s.p.Ctx, held, s.record, step)
		rowSecurityUnchanged(s)
		s.actor("rls_owner")
		predefinedSelect(s, "SELECT id,v FROM rule_cte_event", []string{"id", "v"}, []string{"INT4", "INT4"}, nil)
	})
}
