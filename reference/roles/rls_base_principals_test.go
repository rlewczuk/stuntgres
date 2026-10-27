package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRLSBasePrincipals(t *testing.T) {
	rlsBaseCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "rls_owner", func() {
			s.Exec("CREATE VIEW rls_owner_view AS SELECT id,v FROM t")
			s.Exec("CREATE VIEW rls_invoker_view WITH(security_invoker=true) AS SELECT id,v FROM t")
			s.Exec("CREATE VIEW rls_nested AS SELECT id,v FROM rls_invoker_view")
		})
		s.Exec("GRANT SELECT,INSERT,UPDATE,DELETE ON rls_owner_view,rls_invoker_view,rls_nested TO rls_reader")
		fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "rls_owner", func() { s.Run(fixture.Exec("CREATE MATERIALIZED VIEW rls_mv AS SELECT id FROM t", 2)) })
		s.Exec("GRANT SELECT ON rls_mv TO rls_reader")
		s.Exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		s.Actor("rls_reader")
		predefinedSelect(s, "SELECT id FROM rls_owner_view ORDER BY id", []string{"id"}, []string{"INT4"}, rlsBaseSeedRows())
		predefinedSelect(s, "SELECT id FROM rls_invoker_view", []string{"id"}, []string{"INT4"}, nil)
		predefinedSelect(s, "SELECT id FROM rls_nested", []string{"id"}, []string{"INT4"}, nil)
		s.Run(fixture.Exec("INSERT INTO rls_owner_view VALUES(3,30)", 1))
		rlsBaseProposalDenied(s, "INSERT INTO rls_invoker_view VALUES(4,40)")
		s.Run(fixture.Exec("UPDATE rls_invoker_view SET v=99", 0))
		s.Run(fixture.Exec("DELETE FROM rls_invoker_view", 0))
		predefinedSelect(s, "SELECT id FROM rls_mv ORDER BY id", []string{"id"}, []string{"INT4"}, rlsBaseSeedRows())
		s.Actor("rls_owner")
		s.Exec("ALTER TABLE t FORCE ROW LEVEL SECURITY")
		s.Actor("")
		s.Exec("REFRESH MATERIALIZED VIEW rls_mv")
		predefinedSelect(s, "SELECT id FROM rls_mv", []string{"id"}, []string{"INT4"}, nil)
		s.Actor("rls_owner")
		fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "rls_owner", func() {
			s.Exec("CREATE TABLE rls_parent(id INT) PARTITION BY RANGE(id)")
			s.Exec("CREATE TABLE rls_leaf PARTITION OF rls_parent FOR VALUES FROM(0) TO(10)")
		})
		s.Run(fixture.Exec("INSERT INTO rls_parent VALUES(7)", 1))
		s.Exec("GRANT SELECT,INSERT ON rls_parent,rls_leaf TO rls_reader")
		s.Exec("ALTER TABLE rls_leaf ENABLE ROW LEVEL SECURITY")
		s.Actor("rls_reader")
		predefinedSelect(s, "SELECT id FROM rls_parent", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
		predefinedSelect(s, "SELECT id FROM rls_leaf", []string{"id"}, []string{"INT4"}, nil)
		s.Actor("rls_owner")
		s.Exec("ALTER TABLE rls_leaf DISABLE ROW LEVEL SECURITY")
		s.Exec("ALTER TABLE rls_parent ENABLE ROW LEVEL SECURITY")
		s.Actor("rls_reader")
		predefinedSelect(s, "SELECT id FROM rls_parent", []string{"id"}, []string{"INT4"}, nil)
		predefinedSelect(s, "SELECT id FROM rls_leaf", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
		predefinedDenied(s, "INSERT INTO rls_parent VALUES(8)", `new row violates row-level security policy for table "rls_parent"`, false)
	})
}

func TestReferenceRLSBaseFreshness(t *testing.T) {
	rlsBaseCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		s.Actor("rls_reader")
		held := predefinedHeld(s, "SELECT id FROM t ORDER BY id")
		check := func(stage string, rows [][]any) {
			s.T.Run(stage, func(t *testing.T) {
				fixture.RunStep(t, s.P.Ctx, held, s.Record, fixture.Select(held.SQL, []string{"id"}, []string{"INT4"}, rows))
			})
		}
		check("disabled", rlsBaseSeedRows())
		s.Exec("PREPARE rls_sql AS SELECT id FROM t ORDER BY id")
		s.Actor("rls_owner")
		s.Exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		s.Actor("rls_reader")
		check("enabled", nil)
		predefinedSelect(s, "EXECUTE rls_sql", []string{"id"}, []string{"INT4"}, nil)
		s.Exec("DEALLOCATE rls_sql")
		s.Actor("")
		s.Exec("ALTER ROLE rls_reader BYPASSRLS")
		s.Actor("rls_reader")
		// PostgreSQL 18.6 invalidates role-dependent plans after attribute
		// changes (CVE-2026-14666); both backends observe direct BYPASSRLS.
		check("direct_bypass_attribute_fresh", rlsBaseSeedRows())
		// Exercise an actually unprepared read and a distinct new handle,
		// independently of this scenario's direct/prepared route.
		fixture.RunStep(s.T, s.P.Ctx, s.C, s.Record, fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, rlsBaseSeedRows()))
		fresh := predefinedHeld(s, "SELECT id FROM t ORDER BY id")
		fixture.RunStep(s.T, s.P.Ctx, fresh, s.Record, fixture.Select(fresh.SQL, []string{"id"}, []string{"INT4"}, rlsBaseSeedRows()))
		s.Actor("")
		s.Exec("ALTER ROLE rls_reader NOBYPASSRLS")
		s.Actor("rls_reader")
		check("revoked_bypass_attribute", nil)
		s.Actor("rls_owner")
		s.Exec("ALTER TABLE t DISABLE ROW LEVEL SECURITY")
		s.Actor("rls_reader")
		check("disabled_again", rlsBaseSeedRows())
		// Keep one ordinary actor throughout the transaction: changing role
		// inside an explicit transaction is an existing native boundary.
		s.Actor("rls_owner")
		s.Exec("BEGIN")
		s.Exec("DECLARE rls_cursor CURSOR FOR SELECT id FROM t ORDER BY id")
		if s.Backend.PG {
			predefinedFailure(s, "ALTER TABLE t FORCE ROW LEVEL SECURITY, ENABLE ROW LEVEL SECURITY", fixture.Diagnostic{Code: "55006", Message: `cannot ALTER TABLE "t" because it is being used by active queries in this session`}, false, "execute")
			s.Exec("ROLLBACK")
			s.Exec("BEGIN")
		}
		// Native eager cursors own immutable captured input, rather than a
		// PostgreSQL active executor. Flag changes cannot rewrite that input.
		s.Exec("ALTER TABLE t FORCE ROW LEVEL SECURITY, ENABLE ROW LEVEL SECURITY")
		rlsBaseRows(s, nil)
		if !s.Backend.PG {
			predefinedSelect(s, "FETCH ALL FROM rls_cursor", []string{"id"}, []string{"INT4"}, rlsBaseSeedRows())
			s.Exec("CLOSE rls_cursor")
		}
		s.Exec("ROLLBACK")
	})
}

func TestReferenceRLSBaseFeatureSmoke(t *testing.T) {
	rlsBaseCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		s.Exec("ALTER TABLE t FORCE ROW LEVEL SECURITY")
		rlsBaseRows(s, rlsBaseSeedRows())
		s.Exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		rlsBaseRows(s, nil)
		s.Exec("ALTER TABLE t NO FORCE ROW LEVEL SECURITY")
		rlsBaseRows(s, rlsBaseSeedRows())
		s.Exec("CREATE POLICY smoke_keep ON t TO rls_other USING(TRUE)")
		s.Actor("")
		s.Exec("GRANT pg_read_all_data TO rls_reader")
		s.Actor("rls_reader")
		rlsBaseRows(s, nil)
		predefinedDenied(s, "ALTER TABLE t DISABLE ROW LEVEL SECURITY", "must be owner of table t", false)
		s.Actor("rls_owner")
		s.Exec("ALTER TABLE t DISABLE ROW LEVEL SECURITY")
		s.Exec("ALTER POLICY smoke_keep ON t RENAME TO smoke_retained")
		s.Actor("rls_reader")
		rlsBaseRows(s, rlsBaseSeedRows())
		s.Actor("rls_owner")
		s.Exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY")
		s.Exec("DROP POLICY smoke_retained ON t")
		rlsBaseEnabled(s, true)
		rlsBaseRows(s, nil)
		s.Actor("rls_bypass")
		rlsBaseRows(s, rlsBaseSeedRows())
		s.Actor("")
		rlsBaseRows(s, rlsBaseSeedRows())
	})
}
