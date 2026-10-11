package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceIndexMaintenancePrivileges(t *testing.T) {
	t.Run("public_parent_maintain", func(t *testing.T) {
		fixture.RoleGrantScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
			run(fixture.Exec("CREATE TABLE t(n INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
			run(fixture.Exec("CREATE INDEX ix ON t(n)", 0))
			run(fixture.Exec("GRANT MAINTAIN ON t TO rg_reader", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
			for _, q := range []string{"REINDEX INDEX ix", "REINDEX TABLE t"} {
				run(fixture.Exec(q, 0))
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 0))
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				run(fixture.Exec("REVOKE MAINTAIN ON t FROM rg_reader", 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
				message := "permission denied for table t"
				if q == "REINDEX INDEX ix" {
					message = "permission denied for index ix"
				}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: message}))
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				run(fixture.Exec("GRANT MAINTAIN ON t TO rg_reader", 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
			}
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Select("SELECT n FROM t", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
		})
	})
	fixture.RoleReferenceCleanup(t, []string{"im_actor", "im_owner", "im_schema_owner"})
	fixture.SchemaReferenceCleanup(t, "im_acl")
	indexMaintenanceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, writer := fixture.LockConnections(t, p.Ctx, b.DB)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
		for _, q := range []string{"CREATE USER im_actor", "CREATE USER im_owner", "CREATE USER im_schema_owner", "CREATE SCHEMA im_acl AUTHORIZATION im_schema_owner"} {
			run(fixture.Exec(q, 0))
		}
		if b.PG {
			run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA im_acl TO im_owner", 0))
			run(fixture.Exec("GRANT USAGE ON SCHEMA im_acl TO im_actor", 0))
		}
		if b.PG {
			run(fixture.Exec("SET ROLE im_owner", 0))
		}
		run(fixture.Exec("CREATE TABLE im_acl.t(n INT)", 0))
		run(fixture.Exec("INSERT INTO im_acl.t VALUES(1)", 1))
		run(fixture.Exec("CREATE INDEX ix ON im_acl.t(n)", 0))
		if b.PG {
			run(fixture.Exec("RESET ROLE", 0))
		}
		run(fixture.Exec("SET ROLE im_actor", 0))
		for _, c := range []struct{ q, message string }{
			{"REINDEX INDEX im_acl.ix", "permission denied for index ix"},
			{"REINDEX TABLE im_acl.t", "permission denied for table t"},
			{"ALTER INDEX im_acl.ix RENAME TO fresh", "must be owner of index ix"},
			{"DROP INDEX im_acl.ix", "must be owner of index ix"},
		} {
			// PG grants schema USAGE above. Native has no public schema GRANT;
			// its earlier schema boundary must not be mislabeled as MAINTAIN.
			// Native ACL units establish exact parent MAINTAIN admission/denial.
			if !b.PG {
				c.message = "permission denied for schema im_acl"
			}
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(c.q, fixture.Diagnostic{Code: "42501", Message: c.message})
				s.ErrorStage = "execute"
				if prepared {
					s = fixture.Prepared(s)
				}
				run(s)
			}
		}
		run(fixture.Exec("RESET ROLE", 0))
		// Nonpublic-schema provisioning remains PostgreSQL-only. The public
		// parent-MAINTAIN scenario below exercises real grants on both backends.
		if b.PG {
			run(fixture.Exec("GRANT MAINTAIN ON im_acl.t TO im_actor", 0))
			run(fixture.Exec("SET ROLE im_actor", 0))
			run(fixture.Prepared(fixture.Exec("REINDEX INDEX im_acl.ix", 0)))
			run(fixture.Prepared(fixture.Exec("REINDEX TABLE im_acl.t", 0)))
			run(fixture.Select("SELECT indexname FROM pg_indexes WHERE schemaname='im_acl'", []string{"indexname"}, []string{"NAME"}, [][]any{{"ix"}}))
			stmt, err := a.PrepareContext(p.Ctx, "REINDEX INDEX im_acl.ix")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: a, Statement: stmt, SQL: "REINDEX INDEX im_acl.ix"}
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("REVOKE MAINTAIN ON im_acl.t FROM im_actor", 0))
			run(fixture.Exec("SET ROLE im_actor", 0))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ImplicitError("REINDEX INDEX im_acl.ix", fixture.Diagnostic{Code: "42501", Message: "permission denied for index ix"}))
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("REVOKE CREATE ON SCHEMA im_acl FROM im_owner", 0))
			run(fixture.Exec("SET ROLE im_owner", 0))
			run(fixture.ImplicitError("ALTER INDEX im_acl.ix RENAME TO fresh", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema im_acl"}))
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("SET ROLE im_schema_owner", 0))
			run(fixture.ImplicitError("ALTER INDEX im_acl.ix RENAME TO fresh", fixture.Diagnostic{Code: "42501", Message: "must be owner of index ix"}))
			run(fixture.Exec("DROP INDEX im_acl.ix", 0))
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("CREATE INDEX ix ON im_acl.t(n)", 0))
		}
		// Denied admission must not wait behind a writer on the parent.
		fixture.LockExec(t, p.Ctx, writer, "BEGIN", 0)
		fixture.LockExec(t, p.Ctx, writer, "INSERT INTO im_acl.t VALUES(2)", 1)
		run(fixture.Exec("SET ROLE im_actor", 0))
		run(fixture.Exec("SET lock_timeout='50ms'", 0))
		message := "permission denied for index ix"
		if !b.PG {
			message = "permission denied for schema im_acl"
		}
		run(fixture.ImplicitError("REINDEX INDEX im_acl.ix", fixture.Diagnostic{Code: "42501", Message: message}))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("SET lock_timeout=0", 0))
		fixture.LockExec(t, p.Ctx, writer, "ROLLBACK", 0)
		run(fixture.Select("SELECT n FROM im_acl.t", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
	})
}
