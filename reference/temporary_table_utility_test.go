package reference_test

import "testing"

func TestReferenceTemporaryTableUtilityCommit(t *testing.T) {
	temporaryTableScenario(t, func(s *tempReferenceSession) {
		s.utility("CREATE TEMP TABLE utility_delete(v INT) ON COMMIT DELETE ROWS", nil)
		s.exec(s.c, "INSERT INTO utility_delete VALUES(1)", 1)
		temporaryTableRows(s, s.c, "utility_delete")
		s.utility("CREATE TEMP TABLE utility_drop(v INT) ON COMMIT DROP", nil)
		s.exec(s.c, "CREATE TABLE public.utility_public(v INT)", 0)
		s.exec(s.c, "CREATE INDEX utility_public_index ON public.utility_public(v)", 0)
		s.exec(s.c, "CREATE SCHEMA temporary_utility_schema", 0)
		s.schemas("temporary_utility_schema")
		s.exec(s.c, "SET application_name TO 'temporary utility'", 0)
		s.exec(s.c, "CREATE TABLE IF NOT EXISTS public.utility_public(v TEXT)", 0)
		s.exec(s.c, "DROP TABLE IF EXISTS public.utility_missing", 0)
		temporaryTableRows(s, s.c, "utility_delete")
		s.missing(s.c, "utility_drop", "v", "15")
		s.exec(s.c, "BEGIN", 0)
		s.exec(s.c, "INSERT INTO utility_delete VALUES(2)", 1)
		s.exec(s.c, "SET LOCAL application_name TO 'private temporary utility'", 0)
		temporaryTableRows(s, s.c, "utility_delete", 2)
		s.exec(s.c, "COMMIT", 0)
		temporaryTableRows(s, s.c, "utility_delete")
	})
}

func TestReferenceTemporaryTableCapabilityBoundaries(t *testing.T) {
	temporaryTableScenario(t, func(s *tempReferenceSession) {
		s.utility("CREATE TEMP TABLE boundary_source(v INT)", nil)
		s.exec(s.c, "INSERT INTO boundary_source VALUES(11)", 1)
		d := tempReferenceDiagnostic("0A000", "cannot move objects into or out of temporary schemas", "", "")
		s.utility("ALTER TABLE boundary_source SET SCHEMA public", d)
		temporaryTableRows(s, s.c, "boundary_source", 11)
		if s.postgres {
			s.utility("CREATE VIEW boundary_view AS SELECT v FROM boundary_source", nil)
			temporaryTableRows(s, s.c, "pg_temp.boundary_view", 11)
		} else {
			s.utility("CREATE VIEW boundary_view AS SELECT v FROM boundary_source", tempReferenceDiagnostic("0A000", "temporary views are unsupported", "", ""))
		}
	})
}
