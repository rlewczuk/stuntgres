package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTemporaryTableUtilityCommit(t *testing.T) {
	fixture.TemporaryTableScenario(t, func(s *fixture.TempReferenceSession) {
		s.Utility("CREATE TEMP TABLE utility_delete(v INT) ON COMMIT DELETE ROWS", nil)
		s.Exec(s.C, "INSERT INTO utility_delete VALUES(1)", 1)
		fixture.TemporaryTableRows(s, s.C, "utility_delete")
		s.Utility("CREATE TEMP TABLE utility_drop(v INT) ON COMMIT DROP", nil)
		s.Exec(s.C, "CREATE TABLE public.utility_public(v INT)", 0)
		s.Exec(s.C, "CREATE INDEX utility_public_index ON public.utility_public(v)", 0)
		s.Exec(s.C, "CREATE SCHEMA temporary_utility_schema", 0)
		s.Schemas("temporary_utility_schema")
		s.Exec(s.C, "SET application_name TO 'temporary utility'", 0)
		s.Exec(s.C, "CREATE TABLE IF NOT EXISTS public.utility_public(v TEXT)", 0)
		s.Exec(s.C, "DROP TABLE IF EXISTS public.utility_missing", 0)
		fixture.TemporaryTableRows(s, s.C, "utility_delete")
		s.Missing(s.C, "utility_drop", "v", "15")
		s.Exec(s.C, "BEGIN", 0)
		s.Exec(s.C, "INSERT INTO utility_delete VALUES(2)", 1)
		s.Exec(s.C, "SET LOCAL application_name TO 'private temporary utility'", 0)
		fixture.TemporaryTableRows(s, s.C, "utility_delete", 2)
		s.Exec(s.C, "COMMIT", 0)
		fixture.TemporaryTableRows(s, s.C, "utility_delete")
	})
}

func TestReferenceTemporaryTableCapabilityBoundaries(t *testing.T) {
	fixture.TemporaryTableScenario(t, func(s *fixture.TempReferenceSession) {
		s.Utility("CREATE TEMP TABLE boundary_source(v INT)", nil)
		s.Exec(s.C, "INSERT INTO boundary_source VALUES(11)", 1)
		d := fixture.TempReferenceDiagnostic("0A000", "cannot move objects into or out of temporary schemas", "", "")
		s.Utility("ALTER TABLE boundary_source SET SCHEMA public", d)
		fixture.TemporaryTableRows(s, s.C, "boundary_source", 11)
		if s.Postgres {
			s.Utility("CREATE VIEW boundary_view AS SELECT v FROM boundary_source", nil)
			fixture.TemporaryTableRows(s, s.C, "pg_temp.boundary_view", 11)
		} else {
			s.Utility("CREATE VIEW boundary_view AS SELECT v FROM boundary_source", fixture.TempReferenceDiagnostic("0A000", "temporary views are unsupported", "", ""))
		}
	})
}
