package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// The logical view supplies authority, but the physical table supplies RLS.
// Dead mutations still participate in rewrite and must fail before table ACLs.
func TestReferenceRowSecurityViewMutations(t *testing.T) {
	rlsBaseCases(t, func(s *membershipScenario, _ *sql.DB) {
		fixture.WithPublicSchemaOwner(s.t, s.p, s.backend, "rls_owner", func() {
			s.exec("CREATE VIEW write_def AS SELECT id,v FROM t")
			s.exec("CREATE VIEW write_inv WITH(security_invoker=true) AS SELECT id,v FROM t")
			s.exec("CREATE VIEW write_def_inv AS SELECT id,v FROM write_inv")
			s.exec("CREATE VIEW write_inv_def WITH(security_invoker=true) AS SELECT id,v FROM write_def")
		})
		views := []struct {
			name  string
			owner bool
		}{{"write_def", true}, {"write_inv", false}, {"write_def_inv", false}, {"write_inv_def", true}}
		for _, v := range views {
			s.exec("GRANT SELECT,INSERT,UPDATE,DELETE ON " + v.name + " TO rls_reader")
		}
		s.exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		mutations := []string{
			"INSERT INTO VIEW VALUES(3,30)",
			"INSERT INTO VIEW SELECT 3,30 WHERE false",
			"UPDATE VIEW SET v=99 WHERE id=1",
			"UPDATE VIEW SET v=99 WHERE false",
			"DELETE FROM VIEW WHERE id=1",
			"DELETE FROM VIEW WHERE false",
		}
		// Capture real driver handles with on, before changing the setting or
		// FORCE. Prepare must not execute even an INSERT that would be denied.
		s.actor("rls_reader")
		held := make(map[string]fixture.HeldStatement)
		for _, v := range views {
			for _, template := range mutations {
				q := strings.ReplaceAll(template, "VIEW", v.name)
				held[q] = predefinedHeld(s, q)
			}
		}
		s.exec("SET row_security=off")
		for _, v := range views {
			if v.owner {
				// Non-FORCE definer authority bypasses, without mutating the seed.
				for _, template := range []string{mutations[1], mutations[3], mutations[5]} {
					q := strings.ReplaceAll(template, "VIEW", v.name)
					s.run(fixture.Exec(q, 0))
					fixture.RunStep(s.t, s.p.Ctx, held[q], s.record, fixture.Exec(q, 0))
				}
				continue
			}
			for _, template := range mutations {
				q := strings.ReplaceAll(template, "VIEW", v.name)
				rowSecurityCase(s, v.name+"/ordinary/"+q, func() {
					rowSecurityOff(s, q, "t", false, false)
					rowSecurityHeldDiagnostic(s, held[q], rowSecurityOffDiagnostic("t", false))
				})
			}
		}
		s.actor("rls_owner")
		s.exec("ALTER TABLE t FORCE ROW LEVEL SECURITY")
		s.actor("rls_reader")
		check := func(phase string) {
			for _, v := range views {
				for _, template := range mutations {
					q := strings.ReplaceAll(template, "VIEW", v.name)
					rowSecurityCase(s, phase+"/"+q, func() {
						rowSecurityOff(s, q, "t", false, v.owner)
						rowSecurityHeldDiagnostic(s, held[q], rowSecurityOffDiagnostic("t", v.owner))
					})
				}
			}
		}
		check("force")
		// Both logical and physical ACL denials lose to the rewrite guard.
		s.actor("rls_owner")
		s.exec("REVOKE ALL ON t FROM rls_reader")
		s.actor("rls_reader")
		check("denied_base_acl")
		s.actor("rls_owner")
		for _, v := range views {
			s.exec("REVOKE ALL ON " + v.name + " FROM rls_reader")
		}
		s.actor("rls_reader")
		check("denied_view_and_base_acl")
		// Turning the setting back on restores the normal logical ACL error
		// on the same handles; the off failure did not poison them.
		s.exec("SET row_security=on")
		for _, v := range views {
			q := strings.ReplaceAll(mutations[1], "VIEW", v.name)
			rowSecurityCase(s, "on_acl/"+v.name, func() {
				rowSecurityHeldDiagnostic(s, held[q], fixture.ControlsReferenceDiagnostic("42501", "permission denied for view "+v.name))
			})
		}
		rowSecurityUnchanged(s)
	})
}

func rowSecurityHeldDiagnostic(s *membershipScenario, held fixture.HeldStatement, diagnostic fixture.Diagnostic) {
	s.t.Helper()
	step := fixture.ErrorDiagnostic(held.SQL, diagnostic)
	step.ErrorStage = "execute"
	fixture.RunStep(s.t, s.p.Ctx, held, s.record, step)
}
