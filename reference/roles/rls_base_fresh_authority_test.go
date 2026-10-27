package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRLSBaseFreshAuthority(t *testing.T) {
	rlsBaseCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		s.Actor("rls_reader")
		held := predefinedHeld(s, "SELECT id FROM t ORDER BY id")
		read := func(label string, rows [][]any) {
			s.T.Run(label, func(t *testing.T) {
				fixture.RunStep(t, s.P.Ctx, held, s.Record, fixture.Select(held.SQL, []string{"id"}, []string{"INT4"}, rows))
			})
		}
		read("initial_acl", rlsBaseSeedRows())
		s.Actor("rls_owner")
		s.Exec("REVOKE SELECT ON t FROM rls_reader")
		s.Actor("rls_reader")
		failure := fixture.ErrorDiagnostic(held.SQL, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table t"})
		failure.SelectRows = true
		failure.ErrorStage = "execute"
		fixture.RunStep(s.T, s.P.Ctx, held, s.Record, failure)
		s.Actor("rls_owner")
		s.Exec("GRANT SELECT ON t TO rls_reader")
		s.Actor("rls_reader")
		read("restored_acl", rlsBaseSeedRows())
		s.Actor("rls_owner")
		s.Exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		s.Actor("rls_reader")
		read("enabled_nonowner", nil)
		s.Actor("")
		s.Exec("GRANT rls_owner TO rls_reader WITH INHERIT TRUE")
		s.Actor("rls_reader")
		// PostgreSQL 18.6 invalidates role-dependent plans after membership
		// changes (CVE-2026-14666); both backends observe current inheritance.
		read("inherited_owner_membership_fresh", rlsBaseSeedRows())
		fixture.RunStep(s.T, s.P.Ctx, s.C, s.Record, fixture.Select(held.SQL, []string{"id"}, []string{"INT4"}, rlsBaseSeedRows()))
		fresh := predefinedHeld(s, held.SQL)
		fixture.RunStep(s.T, s.P.Ctx, fresh, s.Record, fixture.Select(fresh.SQL, []string{"id"}, []string{"INT4"}, rlsBaseSeedRows()))
		s.Actor("")
		s.Exec("REVOKE INHERIT OPTION FOR rls_owner FROM rls_reader")
		s.Actor("rls_reader")
		read("revoked_owner_membership", nil)
		fixture.RunStep(s.T, s.P.Ctx, s.C, s.Record, fixture.Select(held.SQL, []string{"id"}, []string{"INT4"}, nil))
		s.Actor("")
		s.Exec("ALTER TABLE t OWNER TO rls_reader")
		s.Actor("rls_reader")
		read("new_direct_owner", rlsBaseSeedRows())
		s.Exec("ALTER TABLE t FORCE ROW LEVEL SECURITY")
		read("forced_new_owner", nil)
	})
}

func TestReferenceRLSBasePredefinedNoInherit(t *testing.T) {
	rlsBaseCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		s.Actor("")
		s.Exec("CREATE SCHEMA pdef_rls AUTHORIZATION rls_owner")
		s.Actor("rls_owner")
		s.Exec("CREATE TABLE pdef_rls.noinherit_t(id INT)")
		s.Run(fixture.Exec("INSERT INTO pdef_rls.noinherit_t VALUES(7)", 1))
		s.Actor("")
		s.Exec("GRANT pg_read_all_data TO rls_other WITH INHERIT FALSE, SET TRUE")
		s.Actor("rls_other")
		predefinedFailure(s, "SELECT id FROM pdef_rls.noinherit_t", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema pdef_rls", Position: "16"}, true, "prepare")
		s.Actor("")
		s.Exec("GRANT pg_read_all_data TO rls_other WITH INHERIT TRUE")
		s.Actor("rls_other")
		predefinedSelect(s, "SELECT id FROM pdef_rls.noinherit_t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
		s.Actor("rls_owner")
		s.Exec("ALTER TABLE pdef_rls.noinherit_t ENABLE ROW LEVEL SECURITY")
		s.Actor("rls_other")
		predefinedSelect(s, "SELECT id FROM pdef_rls.noinherit_t", []string{"id"}, []string{"INT4"}, nil)
	})
}

func TestReferenceRLSBaseInheritanceFlags(t *testing.T) {
	rlsBaseCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "rls_owner", func() { s.Exec("CREATE TABLE rls_child() INHERITS(t)") })
		s.Run(fixture.Exec("INSERT INTO rls_child VALUES(3,30)", 1))
		s.Exec("GRANT SELECT ON rls_child TO rls_reader")
		s.Exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY")
		predefinedSelect(s, "SELECT tablename,rowsecurity FROM pg_tables WHERE schemaname='public' AND (tablename='t' OR tablename='rls_child') ORDER BY tablename", []string{"tablename", "rowsecurity"}, []string{"NAME", "BOOL"}, [][]any{{"rls_child", false}, {"t", true}})
		s.Actor("rls_reader")
		predefinedSelect(s, "SELECT id FROM rls_child", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}})
		s.Actor("rls_owner")
		s.Exec("ALTER TABLE rls_child ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY")
		s.Exec("ALTER TABLE t DISABLE ROW LEVEL SECURITY, NO FORCE ROW LEVEL SECURITY")
		predefinedSelect(s, "SELECT tablename,rowsecurity FROM pg_tables WHERE schemaname='public' AND (tablename='t' OR tablename='rls_child') ORDER BY tablename", []string{"tablename", "rowsecurity"}, []string{"NAME", "BOOL"}, [][]any{{"rls_child", true}, {"t", false}})
		predefinedSelect(s, "SELECT id FROM rls_child", []string{"id"}, []string{"INT4"}, nil)
	})
}
