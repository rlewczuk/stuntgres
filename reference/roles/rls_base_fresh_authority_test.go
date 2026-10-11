package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRLSBaseFreshAuthority(t *testing.T) {
	rlsBaseCases(t, func(s *membershipScenario, db *sql.DB) {
		s.actor("rls_reader")
		held := predefinedHeld(s, "SELECT id FROM t ORDER BY id")
		read := func(label string, rows [][]any) {
			s.t.Run(label, func(t *testing.T) {
				fixture.RunStep(t, s.p.Ctx, held, s.record, fixture.Select(held.SQL, []string{"id"}, []string{"INT4"}, rows))
			})
		}
		read("initial_acl", rlsBaseSeedRows())
		s.actor("rls_owner")
		s.exec("REVOKE SELECT ON t FROM rls_reader")
		s.actor("rls_reader")
		failure := fixture.ErrorDiagnostic(held.SQL, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table t"})
		failure.SelectRows = true
		failure.ErrorStage = "execute"
		fixture.RunStep(s.t, s.p.Ctx, held, s.record, failure)
		s.actor("rls_owner")
		s.exec("GRANT SELECT ON t TO rls_reader")
		s.actor("rls_reader")
		read("restored_acl", rlsBaseSeedRows())
		s.actor("rls_owner")
		s.exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		s.actor("rls_reader")
		read("enabled_nonowner", nil)
		s.actor("")
		s.exec("GRANT rls_owner TO rls_reader WITH INHERIT TRUE")
		s.actor("rls_reader")
		// Pinned PG18 retains this denied plan through this membership grant.
		// Native independently consults current owner inheritance each Execute.
		if s.backend.PG {
			read("inherited_owner_membership_cached_pg", nil)
		} else {
			read("inherited_owner_membership_fresh_native", rlsBaseSeedRows())
		}
		fixture.RunStep(s.t, s.p.Ctx, s.c, s.record, fixture.Select(held.SQL, []string{"id"}, []string{"INT4"}, rlsBaseSeedRows()))
		fresh := predefinedHeld(s, held.SQL)
		fixture.RunStep(s.t, s.p.Ctx, fresh, s.record, fixture.Select(fresh.SQL, []string{"id"}, []string{"INT4"}, rlsBaseSeedRows()))
		s.actor("")
		s.exec("REVOKE INHERIT OPTION FOR rls_owner FROM rls_reader")
		s.actor("rls_reader")
		read("revoked_owner_membership", nil)
		fixture.RunStep(s.t, s.p.Ctx, s.c, s.record, fixture.Select(held.SQL, []string{"id"}, []string{"INT4"}, nil))
		s.actor("")
		if !s.backend.PG {
			predefinedFailure(s, "ALTER TABLE t OWNER TO rls_reader", fixture.Diagnostic{Code: "0A000", Message: "ALTER TABLE action is unsupported", Position: "15"}, false, "prepare")
			rlsBaseRows(s, rlsBaseSeedRows())
			predefinedSelect(s, "SELECT tableowner,rowsecurity FROM pg_tables WHERE schemaname='public' AND tablename='t'", []string{"tableowner", "rowsecurity"}, []string{"NAME", "BOOL"}, [][]any{{"rls_owner", true}})
			s.actor("rls_reader")
			read("unsupported_owner_transfer_preserves_deny", nil)
			return
		}
		s.exec("ALTER TABLE t OWNER TO rls_reader")
		s.actor("rls_reader")
		read("new_direct_owner", rlsBaseSeedRows())
		s.exec("ALTER TABLE t FORCE ROW LEVEL SECURITY")
		read("forced_new_owner", nil)
	})
}

func TestReferenceRLSBasePredefinedNoInherit(t *testing.T) {
	rlsBaseCases(t, func(s *membershipScenario, db *sql.DB) {
		s.actor("")
		s.exec("CREATE SCHEMA pdef_rls AUTHORIZATION rls_owner")
		s.actor("rls_owner")
		s.exec("CREATE TABLE pdef_rls.noinherit_t(id INT)")
		s.run(fixture.Exec("INSERT INTO pdef_rls.noinherit_t VALUES(7)", 1))
		s.actor("")
		s.exec("GRANT pg_read_all_data TO rls_other WITH INHERIT FALSE, SET TRUE")
		s.actor("rls_other")
		predefinedFailure(s, "SELECT id FROM pdef_rls.noinherit_t", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema pdef_rls", Position: "16"}, true, "prepare")
		s.actor("")
		s.exec("GRANT pg_read_all_data TO rls_other WITH INHERIT TRUE")
		s.actor("rls_other")
		predefinedSelect(s, "SELECT id FROM pdef_rls.noinherit_t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
		s.actor("rls_owner")
		s.exec("ALTER TABLE pdef_rls.noinherit_t ENABLE ROW LEVEL SECURITY")
		s.actor("rls_other")
		predefinedSelect(s, "SELECT id FROM pdef_rls.noinherit_t", []string{"id"}, []string{"INT4"}, nil)
	})
}

func TestReferenceRLSBaseInheritanceFlags(t *testing.T) {
	rlsBaseCases(t, func(s *membershipScenario, db *sql.DB) {
		s.exec("CREATE TABLE rls_child() INHERITS(t)")
		s.run(fixture.Exec("INSERT INTO rls_child VALUES(3,30)", 1))
		s.exec("GRANT SELECT ON rls_child TO rls_reader")
		s.exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY")
		predefinedSelect(s, "SELECT tablename,rowsecurity FROM pg_tables WHERE schemaname='public' AND (tablename='t' OR tablename='rls_child') ORDER BY tablename", []string{"tablename", "rowsecurity"}, []string{"NAME", "BOOL"}, [][]any{{"rls_child", false}, {"t", true}})
		s.actor("rls_reader")
		predefinedSelect(s, "SELECT id FROM rls_child", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}})
		s.actor("rls_owner")
		s.exec("ALTER TABLE rls_child ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY")
		s.exec("ALTER TABLE t DISABLE ROW LEVEL SECURITY, NO FORCE ROW LEVEL SECURITY")
		predefinedSelect(s, "SELECT tablename,rowsecurity FROM pg_tables WHERE schemaname='public' AND (tablename='t' OR tablename='rls_child') ORDER BY tablename", []string{"tablename", "rowsecurity"}, []string{"NAME", "BOOL"}, [][]any{{"rls_child", true}, {"t", false}})
		predefinedSelect(s, "SELECT id FROM rls_child", []string{"id"}, []string{"INT4"}, nil)
	})
}
