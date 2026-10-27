package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRowSecurityPrepareAndPrecedence(t *testing.T) {
	rlsBaseCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		s.Exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		s.Actor("rls_reader")
		queries := []struct {
			sql   string
			query bool
		}{
			{"SELECT id FROM t", true}, {"SELECT id FROM t WHERE false", true},
			{"INSERT INTO t VALUES(3,30)", false}, {"UPDATE t SET v=99 WHERE false", false},
			{"DELETE FROM t WHERE false", false}, {"INSERT INTO t SELECT id,v FROM t WHERE false", false},
			{"MERGE INTO t USING (VALUES(3,30)) s(id,v) ON t.id=s.id WHEN NOT MATCHED AND false THEN INSERT VALUES(s.id,s.v)", false},
		}
		// Prepare on is effect-free, including proposals and policy expressions.
		for _, tc := range queries {
			stmt, err := s.C.PrepareContext(s.P.Ctx, tc.sql)
			if err != nil {
				s.T.Fatal(tc.sql, err)
			}
			if err := stmt.Close(); err != nil {
				s.T.Fatal(err)
			}
		}
		s.Exec("SET row_security=off")
		for _, tc := range queries {
			rowSecurityCase(s, "fresh_off/"+tc.sql, func() {
				// Always exercise actual protocol Prepare in both fixture routes.
				stmt, err := s.C.PrepareContext(s.P.Ctx, tc.sql)
				if stmt != nil {
					stmt.Close()
					s.T.Fatal("failed Prepare published a statement")
				}
				d := rowSecurityOffDiagnostic("t", false)
				fixture.AssertDiagnostic(s.T, err, &d)
				rowSecurityOff(s, tc.sql, "t", tc.query, false)
			})
		}
		// The rewrite guard wins before table/column executor ACL checks;
		// schema resolution still precedes rewrite.
		s.Actor("rls_other")
		rowSecurityCase(s, "no_table_acl", func() { rowSecurityOff(s, "SELECT id FROM t", "t", true, false) })
		s.Actor("rls_owner")
		s.Exec("GRANT SELECT(id) ON t TO rls_other")
		s.Actor("rls_other")
		rowSecurityCase(s, "allowed_column", func() { rowSecurityOff(s, "SELECT id FROM t", "t", true, false) })
		rowSecurityCase(s, "denied_column", func() { rowSecurityOff(s, "SELECT v FROM t", "t", true, false) })
		s.Actor("rls_noacl")
		rowSecurityCase(s, "bypass_without_acl", func() {
			predefinedFailure(s, "SELECT id FROM t", fixture.Diagnostic{Code: "42501", Message: "permission denied for table t"}, true, "execute")
		})
		s.Actor("")
		s.Exec("CREATE SCHEMA pdef_rls AUTHORIZATION rls_owner")
		s.Actor("rls_owner")
		s.Exec("CREATE TABLE pdef_rls.private_t(id INT)")
		s.Exec("ALTER TABLE pdef_rls.private_t ENABLE ROW LEVEL SECURITY")
		s.Exec("GRANT SELECT ON pdef_rls.private_t TO rls_reader")
		s.Actor("rls_reader")
		rowSecurityCase(s, "schema_denied", func() {
			predefinedFailure(s, "SELECT id FROM pdef_rls.private_t", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema pdef_rls", Position: "16"}, true, "prepare")
		})
		s.Actor("rls_owner")
		s.Exec("GRANT USAGE ON SCHEMA pdef_rls TO rls_other")
		s.Actor("rls_other")
		rowSecurityCase(s, "schema_allowed_table_denied", func() { rowSecurityOff(s, "SELECT id FROM pdef_rls.private_t", "private_t", true, false) })
		s.Actor("rls_owner")
		s.Exec("ALTER TABLE t FORCE ROW LEVEL SECURITY")
		rowSecurityCase(s, "owner_force_hint", func() { rowSecurityOff(s, "SELECT id FROM t", "t", true, true) })
		s.Exec("ALTER TABLE t NO FORCE ROW LEVEL SECURITY")
		s.Exec("CREATE POLICY setting_precedence ON t FOR SELECT USING(TRUE)")
		s.Actor("rls_reader")
		rowSecurityCase(s, "expression_off_precedes_boundary", func() { rowSecurityOff(s, "SELECT id FROM t", "t", true, false) })
		s.Exec("SET row_security=on")
		stmt, err := s.C.PrepareContext(s.P.Ctx, "SELECT id FROM t")
		if err != nil {
			s.T.Fatal("Prepare evaluated policy", err)
		}
		stmt.Close()
		rlsBaseExpression(s, "SELECT id FROM t ORDER BY id", rlsBaseSeedRows())
		rowSecurityUnchanged(s)
	})
}
