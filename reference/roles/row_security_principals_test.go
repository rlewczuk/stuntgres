package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRowSecurityPrincipals(t *testing.T) {
	rlsBaseCases(t, func(s *membershipScenario, db *sql.DB) {
		fixture.WithPublicSchemaOwner(s.t, s.p, s.backend, "rls_owner", func() {
			s.exec("CREATE VIEW setting_definer AS SELECT id FROM t")
			s.exec("CREATE VIEW setting_invoker WITH(security_invoker=true) AS SELECT id FROM t")
			s.exec("CREATE VIEW setting_nested_definer AS SELECT id FROM setting_invoker")
			s.exec("CREATE VIEW setting_nested_invoker WITH(security_invoker=true) AS SELECT id FROM setting_definer")
			s.run(fixture.Exec("CREATE MATERIALIZED VIEW setting_stored AS SELECT id FROM t", 2))
		})
		s.exec("GRANT SELECT ON setting_definer,setting_invoker,setting_nested_definer,setting_nested_invoker,setting_stored TO rls_reader")
		s.exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		s.exec("SET row_security=off")
		s.actor("rls_reader")
		for _, view := range []string{"setting_definer", "setting_nested_invoker", "setting_stored"} {
			rowSecurityCase(s, view, func() {
				predefinedSelect(s, "SELECT id FROM "+view+" ORDER BY id", []string{"id"}, []string{"INT4"}, rlsBaseSeedRows())
			})
		}
		// The innermost invoker view uses the executing reader, not the
		// enclosing definer's owner. The reverse nesting uses the definer.
		rowSecurityOff(s, "SELECT id FROM setting_nested_definer", "t", true, false)
		rowSecurityOff(s, "SELECT id FROM setting_invoker", "t", true, false)
		s.actor("rls_owner")
		s.exec("ALTER TABLE t FORCE ROW LEVEL SECURITY")
		s.actor("rls_reader")
		rowSecurityOff(s, "SELECT id FROM setting_definer", "t", true, true)
		rowSecurityOff(s, "SELECT id FROM setting_nested_definer", "t", true, false)
		rowSecurityOff(s, "SELECT id FROM setting_nested_invoker", "t", true, true)
		predefinedSelect(s, "SELECT id FROM setting_stored ORDER BY id", []string{"id"}, []string{"INT4"}, rlsBaseSeedRows())
		s.actor("")
		s.exec("GRANT rls_owner TO rls_member WITH INHERIT TRUE, SET FALSE")
		s.actor("rls_member")
		rowSecurityOff(s, "SELECT id FROM t", "t", true, true)
		s.actor("rls_owner")
		s.exec("ALTER TABLE t NO FORCE ROW LEVEL SECURITY")
		s.actor("rls_member")
		rlsBaseRows(s, rlsBaseSeedRows())
		s.actor("")
		s.exec("REVOKE rls_owner FROM rls_member")
		s.exec("GRANT rls_bypass TO rls_member WITH INHERIT TRUE")
		s.actor("rls_member")
		rowSecurityOff(s, "SELECT id FROM t", "t", true, false)
		s.actor("rls_owner")
		s.exec("ALTER TABLE t FORCE ROW LEVEL SECURITY")
		for _, actor := range []string{"rls_bypass", ""} {
			s.actor(actor)
			rlsBaseRows(s, rlsBaseSeedRows())
		}
		rowSecurityUnchanged(s)
	})
}
