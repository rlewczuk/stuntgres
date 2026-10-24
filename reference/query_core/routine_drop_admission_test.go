package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineDropAuthorityAndAdmission(t *testing.T) {
	t.Run("owners and execute-only reader", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"rd_reader", "rd_owner", "rd_schema_owner"})
		fixture.SchemaReferenceCleanup(t, "rd_owned")
		routineDropScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, n *[]fixture.Diagnostic) {
			routineExec(t, p, b, c, "CREATE ROLE rd_reader", "CREATE ROLE rd_owner", "CREATE ROLE rd_schema_owner", "CREATE SCHEMA rd_owned AUTHORIZATION rd_schema_owner", "GRANT USAGE, CREATE ON SCHEMA rd_owned TO rd_owner", "GRANT USAGE ON SCHEMA rd_owned TO rd_reader", "SET SESSION AUTHORIZATION rd_owner", "CREATE FUNCTION rd_owned.f() RETURNS integer LANGUAGE SQL AS $$SELECT 5$$", "RESET SESSION AUTHORIZATION", "GRANT EXECUTE ON FUNCTION rd_owned.f() TO rd_reader", "SET SESSION AUTHORIZATION rd_reader")
			defer func() {
				_, err := c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
				if err != nil {
					t.Error(err)
				}
			}()
			routineValue(t, p, b, c, "SELECT rd_owned.f() AS v", "v", "INT4", int64(5))
			for _, q := range []string{"DROP FUNCTION rd_owned.f()", "DROP FUNCTION IF EXISTS rd_owned.f() CASCADE"} {
				routineDropError(t, p, b, c, q, fixture.ControlsReferenceDiagnostic("42501", "must be owner of function rd_owned.f"))
			}
			routineExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "SET SESSION AUTHORIZATION rd_schema_owner", "DROP FUNCTION rd_owned.f()", "RESET SESSION AUTHORIZATION", "SET SESSION AUTHORIZATION rd_owner", "CREATE FUNCTION rd_owned.f() RETURNS integer LANGUAGE SQL AS $$SELECT 5$$", "DROP ROUTINE rd_owned.f()", "RESET SESSION AUTHORIZATION")
			routineDropNotices(t, n)
		})
	})
	t.Run("prepare read only and explicit transaction", func(t *testing.T) {
		routineDropScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, n *[]fixture.Diagnostic) {
			routineExec(t, p, b, c, "CREATE FUNCTION rd_admit() RETURNS integer LANGUAGE SQL AS $$SELECT 5$$")
			for _, q := range []string{"DROP FUNCTION rd_admit()", "DROP FUNCTION IF EXISTS rd_missing()"} {
				s, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				s.Close()
			}
			routineDropNotices(t, n)
			routineValue(t, p, b, c, "SELECT rd_admit() AS v", "v", "INT4", int64(5))
			for _, kind := range []string{"FUNCTION", "PROCEDURE", "ROUTINE"} {
				routineExec(t, p, b, c, "BEGIN READ ONLY")
				q := "DROP " + kind + " rd_missing()"
				s, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				_, err = s.ExecContext(p.Ctx)
				s.Close()
				fixture.AssertDiagnostic(t, err, new(fixture.ControlsReferenceDiagnostic("25006", "cannot execute DROP "+kind+" in a read-only transaction")))
				routineExec(t, p, b, c, "ROLLBACK")
			}
			routineExec(t, p, b, c, "BEGIN")
			routineExec(t, p, b, c, "DROP FUNCTION rd_admit()")
			routineValueWithNativeNullability(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname='rd_admit'", "v", "INT8", int64(0), false, true)
			routineExec(t, p, b, c, "ROLLBACK")
			routineValue(t, p, b, c, "SELECT rd_admit() AS v", "v", "INT4", int64(5))
			for _, q := range []string{"DROP FUNCTION pg_catalog.abs(integer) CASCADE", "DROP FUNCTION IF EXISTS pg_catalog.abs(integer) CASCADE"} {
				routineDropError(t, p, b, c, q, fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop function abs(integer) because it is required by the database system"))
			}
			routineExec(t, p, b, c, "CREATE FUNCTION public.abs(integer) RETURNS integer LANGUAGE SQL AS $$SELECT 99$$", "DROP FUNCTION public.abs(integer)")
			routineValue(t, p, b, c, "SELECT pg_catalog.abs(-4) AS v", "v", "INT4", int64(4))
			routineDropNotices(t, n)
		})
	})
}
