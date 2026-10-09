package reference_test

import "testing"

func TestReferenceIndexMaintenancePrivileges(t *testing.T) {
	roleReferenceCleanup(t, []string{"im_actor", "im_owner", "im_schema_owner"})
	schemaReferenceCleanup(t, "im_acl")
	indexMaintenanceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		a, writer := referenceLockConnections(t, p.ctx, b.db)
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, a, b.recorder, s) }
		for _, q := range []string{"CREATE USER im_actor", "CREATE USER im_owner", "CREATE USER im_schema_owner", "CREATE SCHEMA im_acl AUTHORIZATION im_schema_owner"} {
			run(referenceExec(q, 0))
		}
		if b.pg {
			run(referenceExec("GRANT USAGE,CREATE ON SCHEMA im_acl TO im_owner", 0))
			run(referenceExec("GRANT USAGE ON SCHEMA im_acl TO im_actor", 0))
		}
		if b.pg {
			run(referenceExec("SET ROLE im_owner", 0))
		}
		run(referenceExec("CREATE TABLE im_acl.t(n INT)", 0))
		run(referenceExec("INSERT INTO im_acl.t VALUES(1)", 1))
		run(referenceExec("CREATE INDEX ix ON im_acl.t(n)", 0))
		if b.pg {
			run(referenceExec("RESET ROLE", 0))
		}
		run(referenceExec("SET ROLE im_actor", 0))
		for _, c := range []struct{ q, message string }{
			{"REINDEX INDEX im_acl.ix", "permission denied for index ix"},
			{"REINDEX TABLE im_acl.t", "permission denied for table t"},
			{"ALTER INDEX im_acl.ix RENAME TO fresh", "must be owner of index ix"},
			{"DROP INDEX im_acl.ix", "must be owner of index ix"},
		} {
			// PG grants schema USAGE above. Native has no public schema GRANT;
			// its earlier schema boundary must not be mislabeled as MAINTAIN.
			// Native ACL units establish exact parent MAINTAIN admission/denial.
			if !b.pg {
				c.message = "permission denied for schema im_acl"
			}
			for _, prepared := range []bool{false, true} {
				s := referenceImplicitError(c.q, referenceDiagnostic{Code: "42501", Message: c.message})
				s.errorStage = "execute"
				if prepared {
					s = referencePrepared(s)
				}
				run(s)
			}
		}
		run(referenceExec("RESET ROLE", 0))
		// Native has no public ACL mutation API/SQL GRANT. Real GRANT witnesses
		// establish the parent MAINTAIN contract; engine ACL units cover native grants.
		if b.pg {
			run(referenceExec("GRANT MAINTAIN ON im_acl.t TO im_actor", 0))
			run(referenceExec("SET ROLE im_actor", 0))
			run(referencePrepared(referenceExec("REINDEX INDEX im_acl.ix", 0)))
			run(referencePrepared(referenceExec("REINDEX TABLE im_acl.t", 0)))
			run(referenceSelect("SELECT indexname FROM pg_indexes WHERE schemaname='im_acl'", []string{"indexname"}, []string{"NAME"}, [][]any{{"ix"}}))
			stmt, err := a.PrepareContext(p.ctx, "REINDEX INDEX im_acl.ix")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: a, statement: stmt, query: "REINDEX INDEX im_acl.ix"}
			run(referenceExec("RESET ROLE", 0))
			run(referenceExec("REVOKE MAINTAIN ON im_acl.t FROM im_actor", 0))
			run(referenceExec("SET ROLE im_actor", 0))
			referenceRunStep(t, p.ctx, held, b.recorder, referenceImplicitError("REINDEX INDEX im_acl.ix", referenceDiagnostic{Code: "42501", Message: "permission denied for index ix"}))
			run(referenceExec("RESET ROLE", 0))
			run(referenceExec("REVOKE CREATE ON SCHEMA im_acl FROM im_owner", 0))
			run(referenceExec("SET ROLE im_owner", 0))
			run(referenceImplicitError("ALTER INDEX im_acl.ix RENAME TO fresh", referenceDiagnostic{Code: "42501", Message: "permission denied for schema im_acl"}))
			run(referenceExec("RESET ROLE", 0))
			run(referenceExec("SET ROLE im_schema_owner", 0))
			run(referenceImplicitError("ALTER INDEX im_acl.ix RENAME TO fresh", referenceDiagnostic{Code: "42501", Message: "must be owner of index ix"}))
			run(referenceExec("DROP INDEX im_acl.ix", 0))
			run(referenceExec("RESET ROLE", 0))
			run(referenceExec("CREATE INDEX ix ON im_acl.t(n)", 0))
		}
		// Denied admission must not wait behind a writer on the parent.
		referenceLockExec(t, p.ctx, writer, "BEGIN", 0)
		referenceLockExec(t, p.ctx, writer, "INSERT INTO im_acl.t VALUES(2)", 1)
		run(referenceExec("SET ROLE im_actor", 0))
		run(referenceExec("SET lock_timeout='50ms'", 0))
		message := "permission denied for index ix"
		if !b.pg {
			message = "permission denied for schema im_acl"
		}
		run(referenceImplicitError("REINDEX INDEX im_acl.ix", referenceDiagnostic{Code: "42501", Message: message}))
		run(referenceExec("RESET ROLE", 0))
		run(referenceExec("SET lock_timeout=0", 0))
		referenceLockExec(t, p.ctx, writer, "ROLLBACK", 0)
		run(referenceSelect("SELECT n FROM im_acl.t", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
	})
}
