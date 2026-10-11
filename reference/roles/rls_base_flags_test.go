package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRLSBaseFlagTransitions(t *testing.T) {
	rlsBaseCases(t, func(s *membershipScenario, db *sql.DB) {
		rlsBaseEnabled(s, false)
		s.exec("ALTER TABLE t FORCE ROW LEVEL SECURITY")
		rlsBaseEnabled(s, false)
		rlsBaseRows(s, rlsBaseSeedRows())
		s.exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		rlsBaseEnabled(s, true)
		rlsBaseRows(s, nil)
		s.exec("ALTER TABLE t NO FORCE ROW LEVEL SECURITY")
		rlsBaseRows(s, rlsBaseSeedRows())
		s.actor("rls_reader")
		rlsBaseRows(s, nil)
		s.actor("rls_owner")
		s.exec("ALTER TABLE t FORCE ROW LEVEL SECURITY, DISABLE ROW LEVEL SECURITY")
		rlsBaseEnabled(s, false)
		rlsBaseRows(s, rlsBaseSeedRows())
		s.exec("ALTER TABLE ONLY (t) ENABLE ROW LEVEL SECURITY")
		rlsBaseRows(s, nil)
		s.exec("ALTER TABLE t* NO FORCE ROW LEVEL SECURITY, DISABLE ROW LEVEL SECURITY, ENABLE ROW LEVEL SECURITY")
		rlsBaseRows(s, rlsBaseSeedRows())
		s.exec("ALTER TABLE t DISABLE ROW LEVEL SECURITY")
		s.actor("rls_reader")
		rlsBaseRows(s, rlsBaseSeedRows())
	})
}

func TestReferenceRLSBaseBypassAndACL(t *testing.T) {
	rlsBaseCases(t, func(s *membershipScenario, db *sql.DB) {
		s.exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		rlsBaseRows(s, rlsBaseSeedRows())
		s.actor("")
		s.exec("GRANT rls_owner TO rls_member")
		s.actor("rls_member")
		rlsBaseRows(s, rlsBaseSeedRows())
		s.actor("rls_owner")
		s.exec("ALTER TABLE t FORCE ROW LEVEL SECURITY")
		rlsBaseRows(s, nil)
		s.actor("rls_member")
		rlsBaseRows(s, nil)
		s.actor("")
		s.exec("REVOKE rls_owner FROM rls_member")
		s.exec("GRANT rls_bypass TO rls_member")
		s.actor("rls_member")
		rlsBaseRows(s, nil)
		s.actor("rls_bypass")
		rlsBaseRows(s, rlsBaseSeedRows())
		s.actor("rls_noacl")
		predefinedDenied(s, "SELECT id FROM t", "permission denied for table t", true)
		s.actor("rls_other")
		predefinedDenied(s, "SELECT id FROM t", "permission denied for table t", true)
		s.actor("")
		rlsBaseRows(s, rlsBaseSeedRows())
	})
}

func TestReferenceRLSBaseDiagnostics(t *testing.T) {
	rlsBaseCases(t, func(s *membershipScenario, db *sql.DB) {
		s.actor("rls_reader")
		for _, action := range []string{"ENABLE", "DISABLE", "FORCE", "NO FORCE"} {
			predefinedDenied(s, "ALTER TABLE t "+action+" ROW LEVEL SECURITY", "must be owner of table t", false)
		}
		s.actor("rls_owner")
		rlsBaseEnabled(s, false)
		predefinedFailure(s, "ALTER TABLE absent ENABLE ROW LEVEL SECURITY", fixture.Diagnostic{Code: "42P01", Message: `relation "absent" does not exist`}, false, "execute")
		s.exec("CREATE VIEW rls_view AS SELECT id FROM t")
		predefinedFailure(s, "ALTER TABLE rls_view ENABLE ROW LEVEL SECURITY", fixture.Diagnostic{Code: "42809", Message: `ALTER action ENABLE ROW SECURITY cannot be performed on relation "rls_view"`, Detail: "This operation is not supported for views."}, false, "execute")
		s.exec("CREATE SEQUENCE rls_seq")
		predefinedFailure(s, "ALTER TABLE rls_seq FORCE ROW LEVEL SECURITY", fixture.Diagnostic{Code: "42809", Message: `ALTER action FORCE ROW SECURITY cannot be performed on relation "rls_seq"`, Detail: "This operation is not supported for sequences."}, false, "execute")
		s.exec("BEGIN READ ONLY")
		predefinedFailure(s, "ALTER TABLE absent ENABLE ROW LEVEL SECURITY", fixture.Diagnostic{Code: "25006", Message: "cannot execute ALTER TABLE in a read-only transaction"}, false, "execute")
		s.exec("ROLLBACK")
		rlsBaseRows(s, rlsBaseSeedRows())
	})
}

func TestReferenceRLSBaseTransactions(t *testing.T) {
	rlsBaseCases(t, func(s *membershipScenario, db *sql.DB) {
		observer, err := db.Conn(s.p.Ctx)
		if err != nil {
			s.t.Fatal(err)
		}
		defer observer.Close()
		peerEnabled := func(enabled bool) {
			step := fixture.Select("SELECT rowsecurity FROM pg_tables WHERE schemaname='public' AND tablename='t'", []string{"rowsecurity"}, []string{"BOOL"}, [][]any{{enabled}})
			step.Prepared = s.prepared
			fixture.RunStep(s.t, s.p.Ctx, observer, s.record, step)
		}
		peerEnabled(false)
		s.exec("BEGIN")
		s.exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY")
		rlsBaseRows(s, nil)
		peerEnabled(false)
		s.exec("SAVEPOINT rls_save")
		s.exec("ALTER TABLE t DISABLE ROW LEVEL SECURITY")
		rlsBaseRows(s, rlsBaseSeedRows())
		s.exec("ROLLBACK TO rls_save")
		rlsBaseRows(s, nil)
		s.exec("ROLLBACK")
		rlsBaseEnabled(s, false)
		rlsBaseRows(s, rlsBaseSeedRows())
		peerEnabled(false)
		s.exec("BEGIN")
		s.exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY")
		s.exec("COMMIT")
		rlsBaseRows(s, nil)
		peerEnabled(true)
		s.exec("ALTER TABLE t DISABLE ROW LEVEL SECURITY, NO FORCE ROW LEVEL SECURITY")
		predefinedFailure(s, "ALTER TABLE t ENABLE ROW LEVEL SECURITY, ALTER COLUMN absent SET NOT NULL", fixture.Diagnostic{Code: "42703", Message: `column "absent" of relation "t" does not exist`}, false, "execute")
		rlsBaseEnabled(s, false)
		rlsBaseRows(s, rlsBaseSeedRows())
	})
}

func TestReferenceRLSBasePolicyPreservation(t *testing.T) {
	rlsBaseCases(t, func(s *membershipScenario, db *sql.DB) {
		s.exec("CREATE POLICY rls_keep ON t TO rls_other USING(id > 0) WITH CHECK(v > 0)")
		s.exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		s.actor("rls_reader")
		rlsBaseRows(s, nil)
		s.actor("rls_owner")
		s.exec("ALTER TABLE t DISABLE ROW LEVEL SECURITY")
		s.exec("ALTER POLICY rls_keep ON t RENAME TO rls_retained")
		s.actor("rls_reader")
		rlsBaseRows(s, rlsBaseSeedRows())
		s.actor("rls_owner")
		s.exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		s.actor("rls_reader")
		rlsBaseRows(s, nil)
	})
}

func TestReferenceRLSBaseLastPolicyDrop(t *testing.T) {
	rlsBaseCases(t, func(s *membershipScenario, db *sql.DB) {
		s.run(fixture.Exec("DELETE FROM t", 2))
		s.run(fixture.Exec("INSERT INTO t VALUES(7,70)", 1))
		seven := [][]any{{int64(7)}}
		s.exec("CREATE POLICY p ON t TO rls_other USING(TRUE)")
		s.exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		rlsBaseRows(s, seven)
		s.actor("rls_reader")
		rlsBaseRows(s, nil)
		s.actor("rls_owner")
		s.exec("DROP POLICY p ON t")
		rlsBaseEnabled(s, true)
		s.actor("rls_reader")
		rlsBaseRows(s, nil)
		s.actor("rls_owner")
		rlsBaseRows(s, seven)
		s.exec("ALTER TABLE t FORCE ROW LEVEL SECURITY")
		rlsBaseRows(s, nil)
		predefinedFailure(s, "DROP POLICY p ON t", fixture.Diagnostic{Code: "42704", Message: `policy "p" for table "t" does not exist`}, false, "execute")
		rlsBaseEnabled(s, true)
		s.actor("rls_bypass")
		rlsBaseRows(s, seven)
		s.actor("")
		rlsBaseRows(s, seven)
		s.actor("rls_owner")
		s.exec("ALTER TABLE t DISABLE ROW LEVEL SECURITY")
		s.actor("rls_reader")
		rlsBaseRows(s, seven)
	})
}
