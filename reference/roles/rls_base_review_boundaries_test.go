package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRLSBaseMixedAlterBoundary(t *testing.T) {
	rlsBaseCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		nullability := func(nullable bool, count int) {
			s.T.Helper()
			// Native exposes actual declared nullable metadata; pq does not.
			// Keep PostgreSQL's existing catalog assertion alongside the shared
			// public ColumnTypes contract and integrity behavior below.
			fixture.TableDDLMetadata(s.T, s.P.Ctx, s.C, s.Backend.PG, s.Prepared, "SELECT v FROM t ORDER BY id", []string{"v"}, []string{"INT4"}, []bool{nullable}, count)
			if s.Backend.PG {
				value := "NO"
				if nullable {
					value = "YES"
				}
				predefinedSelect(s, "SELECT is_nullable::text FROM information_schema.columns WHERE table_schema='public' AND table_name='t' AND column_name='v'", []string{"is_nullable"}, []string{"TEXT"}, [][]any{{value}})
			}
		}
		// DROP NOT NULL runs before SET NOT NULL in PostgreSQL's ALTER phases,
		// not whole-list textual order; the final column must remain required.
		s.Exec("ALTER TABLE t FORCE ROW LEVEL SECURITY, ALTER COLUMN v SET NOT NULL, ALTER COLUMN v DROP NOT NULL, ENABLE ROW LEVEL SECURITY")
		rlsBaseEnabled(s, true)
		nullability(false, 0)
		rlsBaseRows(s, nil)
		s.Actor("")
		predefinedFailure(s, "INSERT INTO t VALUES(3,NULL)", fixture.Diagnostic{Code: "23502", Message: `null value in column "v" of relation "t" violates not-null constraint`, Detail: "Failing row contains (3, null).", Schema: "public", Table: "t", Column: "v"}, false, "execute")
		predefinedSelect(s, "SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}})
		s.Actor("rls_owner")
		s.Exec("ALTER TABLE t NO FORCE ROW LEVEL SECURITY, ALTER COLUMN v DROP NOT NULL, DISABLE ROW LEVEL SECURITY")
		rlsBaseEnabled(s, false)
		nullability(true, 2)
		s.Actor("")
		s.Run(fixture.Exec("INSERT INTO t VALUES(3,NULL)", 1))
		predefinedSelect(s, "SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(3), nil}})
		s.Run(fixture.Exec("DELETE FROM t WHERE id=3", 1))
		predefinedSelect(s, "SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}})
		s.Actor("rls_reader")
		rlsBaseRows(s, rlsBaseSeedRows())
		s.Actor("rls_owner")
		// Disabled state alone cannot prove NO FORCE survived the mixed list.
		s.Exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		rlsBaseRows(s, rlsBaseSeedRows())
		s.Actor("rls_reader")
		rlsBaseRows(s, nil)
	})
}

func TestReferenceRLSBaseEmptyExpressionBoundary(t *testing.T) {
	rlsBaseCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "rls_owner", func() { s.Exec("CREATE TABLE rls_boundary_empty(id INT)") })
		predefinedSelect(s, "SELECT id FROM rls_boundary_empty", []string{"id"}, []string{"INT4"}, nil)
		s.Exec("GRANT SELECT ON rls_boundary_empty TO rls_reader")
		s.Exec("ALTER TABLE rls_boundary_empty ENABLE ROW LEVEL SECURITY")
		s.Exec("CREATE POLICY empty_expression ON rls_boundary_empty FOR SELECT USING(TRUE)")
		s.Actor("rls_reader")
		// An empty source evaluates no row policy expression.
		rlsBaseExpression(s, "SELECT id FROM rls_boundary_empty", nil)
		predefinedSelect(s, "SELECT count(*) FROM rls_boundary_empty", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}})
		s.Actor("rls_owner")
		predefinedSelect(s, "SELECT count(*) FROM rls_boundary_empty", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}})
		predefinedSelect(s, "SELECT rowsecurity FROM pg_tables WHERE schemaname='public' AND tablename='rls_boundary_empty'", []string{"rowsecurity"}, []string{"BOOL"}, [][]any{{true}})
	})
}

func TestReferenceRLSBaseSQLPreparedExpressionBoundary(t *testing.T) {
	rlsBaseCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		s.Exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		s.Exec("CREATE POLICY prepared_expression ON t FOR SELECT USING(TRUE)")
		s.Actor("rls_reader")
		// SQL preparation itself succeeds with applicable policy metadata. This
		// is distinct from the scenario's direct/public-Go prepared route.
		s.Exec("PREPARE rls_boundary_sql AS SELECT id FROM t ORDER BY id")
		rlsBaseExpression(s, "EXECUTE rls_boundary_sql", rlsBaseSeedRows())
		s.Actor("rls_owner")
		s.Exec("ALTER TABLE t DISABLE ROW LEVEL SECURITY")
		rlsBaseEnabled(s, false)
		s.Exec("ALTER POLICY prepared_expression ON t RENAME TO prepared_expression_preserved")
		s.Actor("rls_reader")
		predefinedSelect(s, "EXECUTE rls_boundary_sql", []string{"id"}, []string{"INT4"}, rlsBaseSeedRows())
		s.Exec("DEALLOCATE rls_boundary_sql")
		s.Actor("")
		predefinedSelect(s, "SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}})
	})
}

func TestReferenceRLSBaseReadBeforeProposalBoundary(t *testing.T) {
	rlsBaseCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "rls_owner", func() { s.Exec("CREATE TABLE rls_boundary_source(id INT,v INT)") })
		s.Run(fixture.Exec("INSERT INTO rls_boundary_source VALUES(3,30)", 1))
		s.Exec("GRANT SELECT ON rls_boundary_source TO rls_reader")
		s.Exec("ALTER TABLE rls_boundary_source ENABLE ROW LEVEL SECURITY")
		s.Exec("CREATE POLICY source_expression ON rls_boundary_source FOR SELECT USING(TRUE)")
		s.Exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		failedProposal := func(q string, adjacent bool) {
			s.T.Helper()
			if adjacent && !s.Backend.PG {
				predefinedFailure(s, q, fixture.Diagnostic{Code: "0A000", Message: "row-security policy expression enforcement is unsupported"}, false, "execute")
			} else {
				rlsBaseProposalDenied(s, q)
			}
			s.Actor("")
			predefinedSelect(s, "SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}})
			predefinedSelect(s, "SELECT id,v FROM rls_boundary_source", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(3), int64(30)}})
		}
		s.Actor("rls_reader")
		// A denied INSERT proposal cannot hide required source enforcement.
		failedProposal("INSERT INTO t SELECT id,v FROM rls_boundary_source", false)
		s.Actor("rls_owner")
		s.Exec("ALTER TABLE rls_boundary_source DISABLE ROW LEVEL SECURITY")
		s.Exec("CREATE POLICY target_expression ON t FOR SELECT USING(TRUE)")
		s.Actor("rls_reader")
		// MERGE's target SELECT phase precedes the demanded unmatched INSERT.
		// The source is now unrestricted, isolating target-read enforcement.
		failedProposal("MERGE INTO t USING rls_boundary_source s ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=s.v WHEN NOT MATCHED THEN INSERT VALUES(s.id,s.v)", true)
	})
}
