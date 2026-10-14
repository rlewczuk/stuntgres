package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRLSBasePrincipals(t *testing.T) {
	rlsBaseCases(t, func(s *membershipScenario, db *sql.DB) {
		fixture.WithPublicSchemaOwner(s.t, s.p, s.backend, "rls_owner", func() {
			s.exec("CREATE VIEW rls_owner_view AS SELECT id,v FROM t")
			s.exec("CREATE VIEW rls_invoker_view WITH(security_invoker=true) AS SELECT id,v FROM t")
			s.exec("CREATE VIEW rls_nested AS SELECT id,v FROM rls_invoker_view")
		})
		s.exec("GRANT SELECT,INSERT,UPDATE,DELETE ON rls_owner_view,rls_invoker_view,rls_nested TO rls_reader")
		fixture.WithPublicSchemaOwner(s.t, s.p, s.backend, "rls_owner", func() { s.run(fixture.Exec("CREATE MATERIALIZED VIEW rls_mv AS SELECT id FROM t", 2)) })
		s.exec("GRANT SELECT ON rls_mv TO rls_reader")
		s.exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		s.actor("rls_reader")
		predefinedSelect(s, "SELECT id FROM rls_owner_view ORDER BY id", []string{"id"}, []string{"INT4"}, rlsBaseSeedRows())
		predefinedSelect(s, "SELECT id FROM rls_invoker_view", []string{"id"}, []string{"INT4"}, nil)
		predefinedSelect(s, "SELECT id FROM rls_nested", []string{"id"}, []string{"INT4"}, nil)
		s.run(fixture.Exec("INSERT INTO rls_owner_view VALUES(3,30)", 1))
		rlsBaseProposalDenied(s, "INSERT INTO rls_invoker_view VALUES(4,40)")
		s.run(fixture.Exec("UPDATE rls_invoker_view SET v=99", 0))
		s.run(fixture.Exec("DELETE FROM rls_invoker_view", 0))
		predefinedSelect(s, "SELECT id FROM rls_mv ORDER BY id", []string{"id"}, []string{"INT4"}, rlsBaseSeedRows())
		s.actor("rls_owner")
		s.exec("ALTER TABLE t FORCE ROW LEVEL SECURITY")
		s.actor("")
		s.exec("REFRESH MATERIALIZED VIEW rls_mv")
		predefinedSelect(s, "SELECT id FROM rls_mv", []string{"id"}, []string{"INT4"}, nil)
		s.actor("rls_owner")
		fixture.WithPublicSchemaOwner(s.t, s.p, s.backend, "rls_owner", func() {
			s.exec("CREATE TABLE rls_parent(id INT) PARTITION BY RANGE(id)")
			s.exec("CREATE TABLE rls_leaf PARTITION OF rls_parent FOR VALUES FROM(0) TO(10)")
		})
		s.run(fixture.Exec("INSERT INTO rls_parent VALUES(7)", 1))
		s.exec("GRANT SELECT,INSERT ON rls_parent,rls_leaf TO rls_reader")
		s.exec("ALTER TABLE rls_leaf ENABLE ROW LEVEL SECURITY")
		s.actor("rls_reader")
		predefinedSelect(s, "SELECT id FROM rls_parent", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
		predefinedSelect(s, "SELECT id FROM rls_leaf", []string{"id"}, []string{"INT4"}, nil)
		s.actor("rls_owner")
		s.exec("ALTER TABLE rls_leaf DISABLE ROW LEVEL SECURITY")
		s.exec("ALTER TABLE rls_parent ENABLE ROW LEVEL SECURITY")
		s.actor("rls_reader")
		predefinedSelect(s, "SELECT id FROM rls_parent", []string{"id"}, []string{"INT4"}, nil)
		predefinedSelect(s, "SELECT id FROM rls_leaf", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
		predefinedDenied(s, "INSERT INTO rls_parent VALUES(8)", `new row violates row-level security policy for table "rls_parent"`, false)
	})
}

func TestReferenceRLSBaseFreshness(t *testing.T) {
	rlsBaseCases(t, func(s *membershipScenario, db *sql.DB) {
		s.actor("rls_reader")
		held := predefinedHeld(s, "SELECT id FROM t ORDER BY id")
		check := func(stage string, rows [][]any) {
			s.t.Run(stage, func(t *testing.T) {
				fixture.RunStep(t, s.p.Ctx, held, s.record, fixture.Select(held.SQL, []string{"id"}, []string{"INT4"}, rows))
			})
		}
		check("disabled", rlsBaseSeedRows())
		s.exec("PREPARE rls_sql AS SELECT id FROM t ORDER BY id")
		s.actor("rls_owner")
		s.exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		s.actor("rls_reader")
		check("enabled", nil)
		predefinedSelect(s, "EXECUTE rls_sql", []string{"id"}, []string{"INT4"}, nil)
		s.exec("DEALLOCATE rls_sql")
		s.actor("")
		s.exec("ALTER ROLE rls_reader BYPASSRLS")
		s.actor("rls_reader")
		// Pinned PG18 keeps this already-planned denied statement after a
		// direct role-attribute change; native intentionally checks fresh.
		if s.backend.PG {
			check("direct_bypass_attribute_cached_pg", nil)
		} else {
			check("direct_bypass_attribute_fresh_native", rlsBaseSeedRows())
		}
		// Exercise an actually unprepared read and a distinct new handle,
		// independently of this scenario's direct/prepared route.
		fixture.RunStep(s.t, s.p.Ctx, s.c, s.record, fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, rlsBaseSeedRows()))
		fresh := predefinedHeld(s, "SELECT id FROM t ORDER BY id")
		fixture.RunStep(s.t, s.p.Ctx, fresh, s.record, fixture.Select(fresh.SQL, []string{"id"}, []string{"INT4"}, rlsBaseSeedRows()))
		s.actor("")
		s.exec("ALTER ROLE rls_reader NOBYPASSRLS")
		s.actor("rls_reader")
		check("revoked_bypass_attribute", nil)
		s.actor("rls_owner")
		s.exec("ALTER TABLE t DISABLE ROW LEVEL SECURITY")
		s.actor("rls_reader")
		check("disabled_again", rlsBaseSeedRows())
		// Keep one ordinary actor throughout the transaction: changing role
		// inside an explicit transaction is an existing native boundary.
		s.actor("rls_owner")
		s.exec("BEGIN")
		s.exec("DECLARE rls_cursor CURSOR FOR SELECT id FROM t ORDER BY id")
		if s.backend.PG {
			predefinedFailure(s, "ALTER TABLE t FORCE ROW LEVEL SECURITY, ENABLE ROW LEVEL SECURITY", fixture.Diagnostic{Code: "55006", Message: `cannot ALTER TABLE "t" because it is being used by active queries in this session`}, false, "execute")
			s.exec("ROLLBACK")
			s.exec("BEGIN")
		}
		// Native eager cursors own immutable captured input, rather than a
		// PostgreSQL active executor. Flag changes cannot rewrite that input.
		s.exec("ALTER TABLE t FORCE ROW LEVEL SECURITY, ENABLE ROW LEVEL SECURITY")
		rlsBaseRows(s, nil)
		if !s.backend.PG {
			predefinedSelect(s, "FETCH ALL FROM rls_cursor", []string{"id"}, []string{"INT4"}, rlsBaseSeedRows())
			s.exec("CLOSE rls_cursor")
		}
		s.exec("ROLLBACK")
	})
}

func TestReferenceRLSBaseFeatureSmoke(t *testing.T) {
	rlsBaseCases(t, func(s *membershipScenario, db *sql.DB) {
		s.exec("ALTER TABLE t FORCE ROW LEVEL SECURITY")
		rlsBaseRows(s, rlsBaseSeedRows())
		s.exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		rlsBaseRows(s, nil)
		s.exec("ALTER TABLE t NO FORCE ROW LEVEL SECURITY")
		rlsBaseRows(s, rlsBaseSeedRows())
		s.exec("CREATE POLICY smoke_keep ON t TO rls_other USING(TRUE)")
		s.actor("")
		s.exec("GRANT pg_read_all_data TO rls_reader")
		s.actor("rls_reader")
		rlsBaseRows(s, nil)
		predefinedDenied(s, "ALTER TABLE t DISABLE ROW LEVEL SECURITY", "must be owner of table t", false)
		s.actor("rls_owner")
		s.exec("ALTER TABLE t DISABLE ROW LEVEL SECURITY")
		s.exec("ALTER POLICY smoke_keep ON t RENAME TO smoke_retained")
		s.actor("rls_reader")
		rlsBaseRows(s, rlsBaseSeedRows())
		s.actor("rls_owner")
		s.exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY")
		s.exec("DROP POLICY smoke_retained ON t")
		rlsBaseEnabled(s, true)
		rlsBaseRows(s, nil)
		s.actor("rls_bypass")
		rlsBaseRows(s, rlsBaseSeedRows())
		s.actor("")
		rlsBaseRows(s, rlsBaseSeedRows())
	})
}
