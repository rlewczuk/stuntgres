package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalPoliciesDiagnostics(t *testing.T) {
	transactionalPolicyMatrix(t, []string{"sql", "go"}, func(s *transactionalPolicies, mode string) {
		s.exec("CREATE POLICY p ON t TO tp_reader USING(id<=2) WITH CHECK(allowed)")
		oid := s.oid("t", "p")
		finish := s.begin(mode)
		for _, tc := range []struct{ q, code, message string }{
			{"ALTER POLICY p ON t TO tp_other USING(FALSE) WITH CHECK(1)", "42804", "argument of POLICY must be type boolean, not type integer"},
			{"ALTER POLICY p ON t USING(count(*)>0)", "42803", "aggregate functions are not allowed in policy expressions"},
			{"ALTER POLICY p ON t USING(row_number() OVER()>0)", "42P20", "window functions are not allowed in policy expressions"},
			{"ALTER POLICY p ON t TO tp_missing", "42704", `role "tp_missing" does not exist`},
			{"ALTER POLICY missing ON t", "42704", `policy "missing" for table "t" does not exist`},
			{"ALTER POLICY missing ON t RENAME TO renamed", "42704", `policy "missing" for table "t" does not exist`},
			{"DROP POLICY missing ON t", "42704", `policy "missing" for table "t" does not exist`},
			{"CREATE POLICY p ON t", "42710", `policy "p" for table "t" already exists`},
			{"CREATE POLICY bad ON t FOR SELECT WITH CHECK(TRUE)", "42601", "WITH CHECK cannot be applied to SELECT or DELETE"},
		} {
			s.exec("SAVEPOINT failure")
			s.failure(tc.q, tc.code, tc.message)
			s.run(fixture.ImplicitError("SELECT 1", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
			s.exec("ROLLBACK TO failure")
			s.sameOID("t", "p", oid)
			s.facts("t", "p", "*", true, "tp_reader", "(id <= 2)", "allowed")
			s.ids(1, 2)
			// Valid TO/USING from the failed ALTER must not have installed.
			// CHECK must still reject FALSE after restoring the statement mark.
			s.exec("SET ROLE tp_reader")
			s.failure("INSERT INTO t VALUES(4,FALSE)", "42501", `new row violates row-level security policy for table "t"`)
			s.exec("ROLLBACK TO failure")
			s.exec("RESET ROLE")
			s.exec("RELEASE failure")
		}
		for _, tc := range []struct{ q, noun string }{
			{"CREATE POLICY denied ON t", "table"},
			{"ALTER POLICY p ON t USING(FALSE)", "table"},
			{"ALTER POLICY p ON t RENAME TO denied", "table"},
			{"DROP POLICY p ON t", "relation"},
		} {
			s.exec("SAVEPOINT owner_check")
			s.exec("SET ROLE tp_reader")
			s.failure(tc.q, "42501", "must be owner of "+tc.noun+" t")
			s.exec("ROLLBACK TO owner_check")
			s.exec("RESET ROLE")
			s.sameOID("t", "p", oid)
			s.facts("t", "p", "*", true, "tp_reader", "(id <= 2)", "allowed")
			s.ids(1, 2)
		}
		q := "ALTER POLICY p ON t AS RESTRICTIVE"
		step := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "AS"`, Position: fixture.Position(policyPosition(q, "AS"))})
		step.Prepared = s.held
		step.ErrorStage = "execute"
		if s.held {
			step.ErrorStage = "prepare"
		}
		s.exec("SAVEPOINT syntax")
		s.run(step)
		s.exec("ROLLBACK TO syntax")
		finish(false)
		s.ids(1, 2)
		for _, tc := range []struct{ q, tag string }{
			{"CREATE POLICY q ON missing TO missing_role", "CREATE POLICY"},
			{"ALTER POLICY p ON missing TO missing_role", "ALTER POLICY"},
			{"ALTER POLICY p ON missing RENAME TO q", "ALTER POLICY"},
			{"DROP POLICY p ON missing", "DROP POLICY"},
		} {
			s.exec("BEGIN READ ONLY")
			s.failure(tc.q, "25006", "cannot execute "+tc.tag+" in a read-only transaction")
			s.exec("ROLLBACK")
			s.sameOID("t", "p", oid)
			s.facts("t", "p", "*", true, "tp_reader", "(id <= 2)", "allowed")
		}
	})
	t.Run("notices", func(t *testing.T) {
		fixture.RoleMembershipCases(t, nil, func(s *fixture.RoleMembershipScenario) {
			policySetup(s)
			s.Exec("BEGIN")
			s.Exec("DROP POLICY IF EXISTS absent ON t")
			policyNotice(s, fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `policy "absent" for relation "t" does not exist, skipping`})
			s.Exec("ROLLBACK")
		})
	})
}
