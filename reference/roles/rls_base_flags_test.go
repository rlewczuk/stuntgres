package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRLSBaseFlagTransitions(t *testing.T) {
	rlsBaseCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		rlsBaseEnabled(s, false)
		s.Exec("ALTER TABLE t FORCE ROW LEVEL SECURITY")
		rlsBaseEnabled(s, false)
		rlsBaseRows(s, rlsBaseSeedRows())
		s.Exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		rlsBaseEnabled(s, true)
		rlsBaseRows(s, nil)
		s.Exec("ALTER TABLE t NO FORCE ROW LEVEL SECURITY")
		rlsBaseRows(s, rlsBaseSeedRows())
		s.Actor("rls_reader")
		rlsBaseRows(s, nil)
		s.Actor("rls_owner")
		s.Exec("ALTER TABLE t FORCE ROW LEVEL SECURITY, DISABLE ROW LEVEL SECURITY")
		rlsBaseEnabled(s, false)
		rlsBaseRows(s, rlsBaseSeedRows())
		s.Exec("ALTER TABLE ONLY (t) ENABLE ROW LEVEL SECURITY")
		rlsBaseRows(s, nil)
		s.Exec("ALTER TABLE t* NO FORCE ROW LEVEL SECURITY, DISABLE ROW LEVEL SECURITY, ENABLE ROW LEVEL SECURITY")
		rlsBaseRows(s, rlsBaseSeedRows())
		s.Exec("ALTER TABLE t DISABLE ROW LEVEL SECURITY")
		s.Actor("rls_reader")
		rlsBaseRows(s, rlsBaseSeedRows())
	})
}

func TestReferenceRLSBaseBypassAndACL(t *testing.T) {
	rlsBaseCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		s.Exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		rlsBaseRows(s, rlsBaseSeedRows())
		s.Actor("")
		s.Exec("GRANT rls_owner TO rls_member")
		s.Actor("rls_member")
		rlsBaseRows(s, rlsBaseSeedRows())
		s.Actor("rls_owner")
		s.Exec("ALTER TABLE t FORCE ROW LEVEL SECURITY")
		rlsBaseRows(s, nil)
		s.Actor("rls_member")
		rlsBaseRows(s, nil)
		s.Actor("")
		s.Exec("REVOKE rls_owner FROM rls_member")
		s.Exec("GRANT rls_bypass TO rls_member")
		s.Actor("rls_member")
		rlsBaseRows(s, nil)
		s.Actor("rls_bypass")
		rlsBaseRows(s, rlsBaseSeedRows())
		s.Actor("rls_noacl")
		predefinedDenied(s, "SELECT id FROM t", "permission denied for table t", true)
		s.Actor("rls_other")
		predefinedDenied(s, "SELECT id FROM t", "permission denied for table t", true)
		s.Actor("")
		rlsBaseRows(s, rlsBaseSeedRows())
	})
}

func TestReferenceRLSBaseDiagnostics(t *testing.T) {
	rlsBaseCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		s.Actor("rls_reader")
		for _, action := range []string{"ENABLE", "DISABLE", "FORCE", "NO FORCE"} {
			predefinedDenied(s, "ALTER TABLE t "+action+" ROW LEVEL SECURITY", "must be owner of table t", false)
		}
		s.Actor("rls_owner")
		rlsBaseEnabled(s, false)
		predefinedFailure(s, "ALTER TABLE absent ENABLE ROW LEVEL SECURITY", fixture.Diagnostic{Code: "42P01", Message: `relation "absent" does not exist`}, false, "execute")
		fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "rls_owner", func() { s.Exec("CREATE VIEW rls_view AS SELECT id FROM t") })
		predefinedFailure(s, "ALTER TABLE rls_view ENABLE ROW LEVEL SECURITY", fixture.Diagnostic{Code: "42809", Message: `ALTER action ENABLE ROW SECURITY cannot be performed on relation "rls_view"`, Detail: "This operation is not supported for views."}, false, "execute")
		fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "rls_owner", func() { s.Exec("CREATE SEQUENCE rls_seq") })
		predefinedFailure(s, "ALTER TABLE rls_seq FORCE ROW LEVEL SECURITY", fixture.Diagnostic{Code: "42809", Message: `ALTER action FORCE ROW SECURITY cannot be performed on relation "rls_seq"`, Detail: "This operation is not supported for sequences."}, false, "execute")
		s.Exec("BEGIN READ ONLY")
		predefinedFailure(s, "ALTER TABLE absent ENABLE ROW LEVEL SECURITY", fixture.Diagnostic{Code: "25006", Message: "cannot execute ALTER TABLE in a read-only transaction"}, false, "execute")
		s.Exec("ROLLBACK")
		rlsBaseRows(s, rlsBaseSeedRows())
	})
}

func TestReferenceRLSBaseTransactions(t *testing.T) {
	rlsBaseCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		observer, err := db.Conn(s.P.Ctx)
		if err != nil {
			s.T.Fatal(err)
		}
		defer observer.Close()
		peerEnabled := func(enabled bool) {
			step := fixture.Select("SELECT rowsecurity FROM pg_tables WHERE schemaname='public' AND tablename='t'", []string{"rowsecurity"}, []string{"BOOL"}, [][]any{{enabled}})
			step.Prepared = s.Prepared
			fixture.RunStep(s.T, s.P.Ctx, observer, s.Record, step)
		}
		peerEnabled(false)
		s.Exec("BEGIN")
		s.Exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY")
		rlsBaseRows(s, nil)
		peerEnabled(false)
		s.Exec("SAVEPOINT rls_save")
		s.Exec("ALTER TABLE t DISABLE ROW LEVEL SECURITY")
		rlsBaseRows(s, rlsBaseSeedRows())
		s.Exec("ROLLBACK TO rls_save")
		rlsBaseRows(s, nil)
		s.Exec("ROLLBACK")
		rlsBaseEnabled(s, false)
		rlsBaseRows(s, rlsBaseSeedRows())
		peerEnabled(false)
		s.Exec("BEGIN")
		s.Exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY")
		s.Exec("COMMIT")
		rlsBaseRows(s, nil)
		peerEnabled(true)
		s.Exec("ALTER TABLE t DISABLE ROW LEVEL SECURITY, NO FORCE ROW LEVEL SECURITY")
		predefinedFailure(s, "ALTER TABLE t ENABLE ROW LEVEL SECURITY, ALTER COLUMN absent SET NOT NULL", fixture.Diagnostic{Code: "42703", Message: `column "absent" of relation "t" does not exist`}, false, "execute")
		rlsBaseEnabled(s, false)
		rlsBaseRows(s, rlsBaseSeedRows())
	})
}

func TestReferenceRLSBasePolicyPreservation(t *testing.T) {
	rlsBaseCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		s.Exec("CREATE POLICY rls_keep ON t TO rls_other USING(id > 0) WITH CHECK(v > 0)")
		s.Exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		s.Actor("rls_reader")
		rlsBaseRows(s, nil)
		s.Actor("rls_owner")
		s.Exec("ALTER TABLE t DISABLE ROW LEVEL SECURITY")
		s.Exec("ALTER POLICY rls_keep ON t RENAME TO rls_retained")
		s.Actor("rls_reader")
		rlsBaseRows(s, rlsBaseSeedRows())
		s.Actor("rls_owner")
		s.Exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		s.Actor("rls_reader")
		rlsBaseRows(s, nil)
	})
}

func TestReferenceRLSBaseLastPolicyDrop(t *testing.T) {
	rlsBaseCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		s.Run(fixture.Exec("DELETE FROM t", 2))
		s.Run(fixture.Exec("INSERT INTO t VALUES(7,70)", 1))
		seven := [][]any{{int64(7)}}
		s.Exec("CREATE POLICY p ON t TO rls_other USING(TRUE)")
		s.Exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		rlsBaseRows(s, seven)
		s.Actor("rls_reader")
		rlsBaseRows(s, nil)
		s.Actor("rls_owner")
		s.Exec("DROP POLICY p ON t")
		rlsBaseEnabled(s, true)
		s.Actor("rls_reader")
		rlsBaseRows(s, nil)
		s.Actor("rls_owner")
		rlsBaseRows(s, seven)
		s.Exec("ALTER TABLE t FORCE ROW LEVEL SECURITY")
		rlsBaseRows(s, nil)
		predefinedFailure(s, "DROP POLICY p ON t", fixture.Diagnostic{Code: "42704", Message: `policy "p" for table "t" does not exist`}, false, "execute")
		rlsBaseEnabled(s, true)
		s.Actor("rls_bypass")
		rlsBaseRows(s, seven)
		s.Actor("")
		rlsBaseRows(s, seven)
		s.Actor("rls_owner")
		s.Exec("ALTER TABLE t DISABLE ROW LEVEL SECURITY")
		s.Actor("rls_reader")
		rlsBaseRows(s, seven)
	})
}
