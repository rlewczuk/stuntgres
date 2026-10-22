package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRLSBaseMixedAlterBoundary(t *testing.T) {
	rlsBaseCases(t, func(s *membershipScenario, db *sql.DB) {
		nullability := func(nullable bool, count int) {
			s.t.Helper()
			// Native exposes actual declared nullable metadata; pq does not.
			// Keep PostgreSQL's existing catalog assertion alongside the shared
			// public ColumnTypes contract and integrity behavior below.
			fixture.TableDDLMetadata(s.t, s.p.Ctx, s.c, s.backend.PG, s.prepared, "SELECT v FROM t ORDER BY id", []string{"v"}, []string{"INT4"}, []bool{nullable}, count)
			if s.backend.PG {
				value := "NO"
				if nullable {
					value = "YES"
				}
				predefinedSelect(s, "SELECT is_nullable::text FROM information_schema.columns WHERE table_schema='public' AND table_name='t' AND column_name='v'", []string{"is_nullable"}, []string{"TEXT"}, [][]any{{value}})
			}
		}
		// DROP NOT NULL runs before SET NOT NULL in PostgreSQL's ALTER phases,
		// not whole-list textual order; the final column must remain required.
		s.exec("ALTER TABLE t FORCE ROW LEVEL SECURITY, ALTER COLUMN v SET NOT NULL, ALTER COLUMN v DROP NOT NULL, ENABLE ROW LEVEL SECURITY")
		rlsBaseEnabled(s, true)
		nullability(false, 0)
		rlsBaseRows(s, nil)
		s.actor("")
		predefinedFailure(s, "INSERT INTO t VALUES(3,NULL)", fixture.Diagnostic{Code: "23502", Message: `null value in column "v" of relation "t" violates not-null constraint`, Detail: "Failing row contains (3, null).", Schema: "public", Table: "t", Column: "v"}, false, "execute")
		predefinedSelect(s, "SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}})
		s.actor("rls_owner")
		s.exec("ALTER TABLE t NO FORCE ROW LEVEL SECURITY, ALTER COLUMN v DROP NOT NULL, DISABLE ROW LEVEL SECURITY")
		rlsBaseEnabled(s, false)
		nullability(true, 2)
		s.actor("")
		s.run(fixture.Exec("INSERT INTO t VALUES(3,NULL)", 1))
		predefinedSelect(s, "SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(3), nil}})
		s.run(fixture.Exec("DELETE FROM t WHERE id=3", 1))
		predefinedSelect(s, "SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}})
		s.actor("rls_reader")
		rlsBaseRows(s, rlsBaseSeedRows())
		s.actor("rls_owner")
		// Disabled state alone cannot prove NO FORCE survived the mixed list.
		s.exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		rlsBaseRows(s, rlsBaseSeedRows())
		s.actor("rls_reader")
		rlsBaseRows(s, nil)
	})
}

func TestReferenceRLSBaseEmptyExpressionBoundary(t *testing.T) {
	rlsBaseCases(t, func(s *membershipScenario, db *sql.DB) {
		fixture.WithPublicSchemaOwner(s.t, s.p, s.backend, "rls_owner", func() { s.exec("CREATE TABLE rls_boundary_empty(id INT)") })
		predefinedSelect(s, "SELECT id FROM rls_boundary_empty", []string{"id"}, []string{"INT4"}, nil)
		s.exec("GRANT SELECT ON rls_boundary_empty TO rls_reader")
		s.exec("ALTER TABLE rls_boundary_empty ENABLE ROW LEVEL SECURITY")
		s.exec("CREATE POLICY empty_expression ON rls_boundary_empty FOR SELECT USING(TRUE)")
		s.actor("rls_reader")
		// An empty source evaluates no row policy expression.
		rlsBaseExpression(s, "SELECT id FROM rls_boundary_empty", nil)
		predefinedSelect(s, "SELECT count(*) FROM rls_boundary_empty", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}})
		s.actor("rls_owner")
		predefinedSelect(s, "SELECT count(*) FROM rls_boundary_empty", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}})
		predefinedSelect(s, "SELECT rowsecurity FROM pg_tables WHERE schemaname='public' AND tablename='rls_boundary_empty'", []string{"rowsecurity"}, []string{"BOOL"}, [][]any{{true}})
	})
}

func TestReferenceRLSBaseSQLPreparedExpressionBoundary(t *testing.T) {
	rlsBaseCases(t, func(s *membershipScenario, db *sql.DB) {
		s.exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		s.exec("CREATE POLICY prepared_expression ON t FOR SELECT USING(TRUE)")
		s.actor("rls_reader")
		// SQL preparation itself succeeds with applicable policy metadata. This
		// is distinct from the scenario's direct/public-Go prepared route.
		s.exec("PREPARE rls_boundary_sql AS SELECT id FROM t ORDER BY id")
		rlsBaseExpression(s, "EXECUTE rls_boundary_sql", rlsBaseSeedRows())
		s.actor("rls_owner")
		s.exec("ALTER TABLE t DISABLE ROW LEVEL SECURITY")
		rlsBaseEnabled(s, false)
		s.exec("ALTER POLICY prepared_expression ON t RENAME TO prepared_expression_preserved")
		s.actor("rls_reader")
		predefinedSelect(s, "EXECUTE rls_boundary_sql", []string{"id"}, []string{"INT4"}, rlsBaseSeedRows())
		s.exec("DEALLOCATE rls_boundary_sql")
		s.actor("")
		predefinedSelect(s, "SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}})
	})
}

func TestReferenceRLSBaseReadBeforeProposalBoundary(t *testing.T) {
	rlsBaseCases(t, func(s *membershipScenario, db *sql.DB) {
		fixture.WithPublicSchemaOwner(s.t, s.p, s.backend, "rls_owner", func() { s.exec("CREATE TABLE rls_boundary_source(id INT,v INT)") })
		s.run(fixture.Exec("INSERT INTO rls_boundary_source VALUES(3,30)", 1))
		s.exec("GRANT SELECT ON rls_boundary_source TO rls_reader")
		s.exec("ALTER TABLE rls_boundary_source ENABLE ROW LEVEL SECURITY")
		s.exec("CREATE POLICY source_expression ON rls_boundary_source FOR SELECT USING(TRUE)")
		s.exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		failedProposal := func(q string, adjacent bool) {
			s.t.Helper()
			if adjacent && !s.backend.PG {
				predefinedFailure(s, q, fixture.Diagnostic{Code: "0A000", Message: "row-security policy expression enforcement is unsupported"}, false, "execute")
			} else {
				rlsBaseProposalDenied(s, q)
			}
			s.actor("")
			predefinedSelect(s, "SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}})
			predefinedSelect(s, "SELECT id,v FROM rls_boundary_source", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(3), int64(30)}})
		}
		s.actor("rls_reader")
		// A denied INSERT proposal cannot hide required source enforcement.
		failedProposal("INSERT INTO t SELECT id,v FROM rls_boundary_source", false)
		s.actor("rls_owner")
		s.exec("ALTER TABLE rls_boundary_source DISABLE ROW LEVEL SECURITY")
		s.exec("CREATE POLICY target_expression ON t FOR SELECT USING(TRUE)")
		s.actor("rls_reader")
		// MERGE's target SELECT phase precedes the demanded unmatched INSERT.
		// The source is now unrestricted, isolating target-read enforcement.
		failedProposal("MERGE INTO t USING rls_boundary_source s ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=s.v WHEN NOT MATCHED THEN INSERT VALUES(s.id,s.v)", true)
	})
}
