package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRowSecurityRuleActions(t *testing.T) {
	rlsBaseCases(t, func(s *membershipScenario, _ *sql.DB) {
		fixture.WithPublicSchemaOwner(s.t, s.p, s.backend, "rls_owner", func() {
			s.exec("CREATE TABLE rule_event(id INT,v INT)")
		})
		// This authority witness does not exercise rule-template DEFAULT
		// refresh; supplied NEW.v must remain the independently expected 30.
		s.exec("ALTER TABLE t ALTER COLUMN v DROP DEFAULT")
		s.exec("CREATE RULE route AS ON INSERT TO rule_event DO ALSO INSERT INTO t VALUES(NEW.id,NEW.v)")
		s.exec("GRANT INSERT ON rule_event TO rls_reader,rls_bypass")
		s.exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		s.actor("rls_reader")
		s.exec("SET row_security=off")
		// Caller has no bypass, but the event owner owns the action target.
		// Both fresh Prepare and real execution must honor action authority.
		const insert = "INSERT INTO rule_event VALUES(3,30)"
		s.run(fixture.Exec(insert, 1))
		s.actor("rls_bypass")
		predefinedSelect(s, "SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(3), int64(30)}})
		s.actor("rls_owner")
		s.run(fixture.Exec("DELETE FROM rule_event", 1))
		s.run(fixture.Exec("DELETE FROM t WHERE id=3", 1))
		s.actor("rls_reader")
		s.exec("SET row_security=on")
		held := predefinedHeld(s, insert)
		dead := predefinedHeld(s, "INSERT INTO rule_event SELECT 3,30 WHERE false")
		s.exec("SET row_security=off")
		fixture.RunStep(s.t, s.p.Ctx, dead, s.record, fixture.Exec(dead.SQL, 0))
		fixture.RunStep(s.t, s.p.Ctx, held, s.record, fixture.Exec(insert, 1))
		s.actor("rls_owner")
		s.run(fixture.Exec("DELETE FROM rule_event", 1))
		s.run(fixture.Exec("DELETE FROM t WHERE id=3", 1))
		s.exec("ALTER TABLE t FORCE ROW LEVEL SECURITY")
		s.actor("rls_reader")
		s.exec("SET row_security=off")
		check := func(phase string, owner bool) {
			for _, h := range []fixture.HeldStatement{held, dead} {
				rowSecurityCase(s, phase+"/"+h.SQL, func() {
					rowSecurityOff(s, h.SQL, "t", false, owner)
					rowSecurityHeldDiagnostic(s, h, rowSecurityOffDiagnostic("t", owner))
				})
			}
		}
		check("force_action_owner", true)
		s.actor("rls_owner")
		s.exec("REVOKE INSERT ON rule_event FROM rls_reader")
		s.actor("rls_reader")
		check("original_acl_denied", true)
		s.exec("SET row_security=on")
		rowSecurityHeldDiagnostic(s, held, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table rule_event"))
		// A bypassing caller cannot lend BYPASSRLS to a nonowner action.
		s.actor("")
		s.exec("GRANT INSERT ON t TO rls_other")
		s.exec("ALTER TABLE rule_event OWNER TO rls_other")
		s.actor("rls_bypass")
		s.exec("SET row_security=off")
		check("caller_bypass_action_nonowner", false)
		// Action table ACL denial must likewise follow the off guard.
		s.actor("")
		s.exec("REVOKE INSERT ON t FROM rls_other")
		s.actor("rls_bypass")
		check("action_acl_denied", false)
		s.exec("SET row_security=on")
		rowSecurityHeldDiagnostic(s, held, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table t"))
		rowSecurityUnchanged(s)
		s.actor("")
		predefinedSelect(s, "SELECT id,v FROM rule_event", []string{"id", "v"}, []string{"INT4", "INT4"}, nil)
	})
}

// INSTEAD NOTHING leaves no executor command, hence no original RLS or ACL
// check. Nonempty replacement retains original RLS obligations even when the
// original write itself is suppressed; its off guard precedes executor ACLs.
func TestReferenceRowSecurityRuleOriginalOrdering(t *testing.T) {
	rlsBaseCases(t, func(s *membershipScenario, _ *sql.DB) {
		fixture.WithPublicSchemaOwner(s.t, s.p, s.backend, "rls_owner", func() {
			s.exec("CREATE TABLE rule_sink(id INT,v INT)")
		})
		s.exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		s.exec("CREATE RULE route AS ON INSERT TO t DO INSTEAD NOTHING")
		s.actor("rls_reader")
		s.exec("SET row_security=off")
		const q = "INSERT INTO t VALUES(3,30)"
		held := predefinedHeld(s, q)
		closeHeld := func() {
			s.t.Helper()
			if err := held.Statement.Close(); err != nil {
				s.t.Fatal(err)
			}
		}
		s.run(fixture.Exec(q, 0))
		fixture.RunStep(s.t, s.p.Ctx, held, s.record, fixture.Exec(q, 0))
		s.actor("rls_owner")
		s.exec("REVOKE INSERT ON t FROM rls_reader")
		s.actor("rls_reader")
		// The pinned oracle succeeds even without original INSERT privilege
		// when unconditional suppression leaves no executable commands.
		s.run(fixture.Exec(q, 0))
		fixture.RunStep(s.t, s.p.Ctx, held, s.record, fixture.Exec(q, 0))
		closeHeld()
		s.actor("rls_owner")
		s.exec("GRANT INSERT ON t TO rls_reader")
		s.exec("CREATE OR REPLACE RULE route AS ON INSERT TO t DO INSTEAD INSERT INTO rule_sink VALUES(NEW.id,NEW.v)")
		s.actor("rls_reader")
		// A handle prepared for an empty program need not depend on the
		// event relation. Prepare each nonempty shape independently; only
		// settings and ACLs change while that phase's handle is held.
		s.exec("SET row_security=on")
		held = predefinedHeld(s, q)
		s.exec("SET row_security=off")
		rowSecurityOff(s, q, "t", false, false)
		rowSecurityHeldDiagnostic(s, held, rowSecurityOffDiagnostic("t", false))
		s.actor("rls_owner")
		predefinedSelect(s, "SELECT id,v FROM rule_sink ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, nil)
		s.exec("REVOKE INSERT ON t FROM rls_reader")
		s.actor("rls_reader")
		rowSecurityOff(s, q, "t", false, false)
		rowSecurityHeldDiagnostic(s, held, rowSecurityOffDiagnostic("t", false))
		closeHeld()
		s.actor("rls_owner")
		// rewriteRuleAction retains the original range-table and permission
		// entries even when the action never references NEW or OLD. RLS is
		// checked during rewrite; original INSERT ACL is checked at execution.
		s.exec("CREATE OR REPLACE RULE route AS ON INSERT TO t DO INSTEAD INSERT INTO rule_sink VALUES(4,40)")
		s.actor("rls_reader")
		s.exec("SET row_security=on")
		held = predefinedHeld(s, q)
		s.exec("SET row_security=off")
		rowSecurityOff(s, q, "t", false, false)
		rowSecurityHeldDiagnostic(s, held, rowSecurityOffDiagnostic("t", false))
		s.exec("SET row_security=on")
		predefinedDenied(s, q, "permission denied for table t", false)
		rowSecurityHeldDiagnostic(s, held, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table t"))
		s.actor("rls_owner")
		predefinedSelect(s, "SELECT id,v FROM rule_sink", []string{"id", "v"}, []string{"INT4", "INT4"}, nil)
		s.exec("GRANT INSERT ON t TO rls_reader")
		s.actor("rls_reader")
		s.run(fixture.Exec(q, 1))
		fixture.RunStep(s.t, s.p.Ctx, held, s.record, fixture.Exec(q, 1))
		closeHeld()
		s.actor("rls_owner")
		predefinedSelect(s, "SELECT id,v FROM rule_sink", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(4), int64(40)}, {int64(4), int64(40)}})
		s.run(fixture.Exec("DELETE FROM rule_sink", 2))
		s.exec("REVOKE INSERT ON t FROM rls_reader")
		s.exec("CREATE OR REPLACE RULE route AS ON INSERT TO t DO ALSO INSERT INTO rule_sink VALUES(NEW.id,NEW.v)")
		s.actor("rls_reader")
		s.exec("SET row_security=on")
		held = predefinedHeld(s, q)
		s.exec("SET row_security=off")
		rowSecurityOff(s, q, "t", false, false)
		rowSecurityHeldDiagnostic(s, held, rowSecurityOffDiagnostic("t", false))
		s.actor("rls_owner")
		// No earlier action can publish when a later original guard fails.
		predefinedSelect(s, "SELECT id,v FROM rule_sink ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, nil)
		rowSecurityUnchanged(s)
	})
}
