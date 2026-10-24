package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalRoutinesDependencies(t *testing.T) {
	t.Run("private-effects", transactionalRoutinePrivateEffects)
	t.Run("definition-errors", transactionalRoutineDefinitionErrors)
	t.Run("definition-privileges", transactionalRoutineDefinitionPrivileges)
	t.Run("selection-validation-demand", func(t *testing.T) {
		routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			routineExec(t, p, b, c,
				"CREATE FUNCTION routine_unit(bigint) RETURNS integer LANGUAGE SQL AS $$SELECT 8$$",
				"CREATE FUNCTION routine_unit(integer) RETURNS integer LANGUAGE SQL AS $$SELECT 4$$",
				"CREATE TABLE routine_unit_rows(x integer)")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO routine_unit_rows VALUES(0),(1)", 2))
			routineExec(t, p, b, c, "CREATE FUNCTION routine_unit_first() RETURNS integer LANGUAGE SQL AS $$SELECT 10/x FROM (SELECT x FROM routine_unit_rows ORDER BY x DESC) q$$")
			routineValue(t, p, b, c, "SELECT routine_unit(1) AS v", "v", "INT4", int64(4))
			routineValue(t, p, b, c, "SELECT routine_unit(1::bigint) AS v", "v", "INT4", int64(8))
			routineValue(t, p, b, c, "SELECT routine_unit_first() AS v", "v", "INT4", int64(10))
			routineExec(t, p, b, c, "CREATE SEQUENCE routine_unit_sequence", "CREATE FUNCTION routine_unit_next() RETURNS bigint LANGUAGE SQL AS $$SELECT nextval('routine_unit_sequence')$$", "CREATE FUNCTION routine_unit_division() RETURNS integer LANGUAGE SQL AS $$SELECT 1/0$$")
			first := fixture.Select("SELECT routine_unit_next() AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}})
			first.CheckMetadata = routineScalarMetadata(b, "v", "INT8", false, false)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, first)
			routineExec(t, p, b, c, "CREATE TABLE routine_unit_defaults(v bigint DEFAULT routine_unit_next()+1)", "CREATE TABLE routine_unit_error_default(v integer DEFAULT routine_unit_division())")
			second := fixture.Select("SELECT routine_unit_next() AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(2)}})
			second.CheckMetadata = routineScalarMetadata(b, "v", "INT8", false, false)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, second)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO routine_unit_defaults DEFAULT VALUES", 1))
			routineValueWithNativeNullability(t, p, b, c, "SELECT v FROM routine_unit_defaults", "v", "INT8", int64(4), true, true)
			q := "CREATE FUNCTION routine_unit_bad() RETURNS integer LANGUAGE SQL AS $$SELECT 'x'::text$$"
			d := fixture.ControlsReferenceDiagnostic("42P13", "return type mismatch in function declared to return integer")
			d.Detail, d.Context = "Actual return type is text.", "SQL function \"routine_unit_bad\""
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic(q, d))
			transactionalRoutineCount(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname='routine_unit_bad'", 0)
			q = "CREATE TABLE routine_unit_unclosed(v float8 DEFAULT ((random())+routine_unit_next()))"
			if b.PG {
				routineExec(t, p, b, c, q)
			} else {
				d = fixture.ControlsReferenceDiagnostic("0A000", "functions are unsupported")
				d.Position = "55"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic(q, d))
				transactionalRoutineCount(t, p, b, c, "SELECT count(*) AS v FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='routine_unit_unclosed'", 0)
			}
		})
	})
	t.Run("hard-soft-and-rollback", func(t *testing.T) {
		routineDropScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
			routineExec(t, p, b, c, "CREATE FUNCTION txr_base(integer) RETURNS integer RETURN $1+1", "CREATE FUNCTION txr_soft() RETURNS integer LANGUAGE SQL AS $$SELECT txr_base(4)$$", "BEGIN", "CREATE FUNCTION txr_hard() RETURNS integer RETURN txr_base(4)", "CREATE FUNCTION txr_default(x integer DEFAULT txr_base(4)) RETURNS integer RETURN x", "SAVEPOINT graph")
			routineValue(t, p, b, c, "SELECT txr_hard() AS v", "v", "INT4", int64(5))
			routineValue(t, p, b, c, "SELECT txr_default() AS v", "v", "INT4", int64(5))
			routineExec(t, p, b, c, "DROP FUNCTION txr_base(integer) CASCADE")
			transactionalRoutineCount(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname IN ('txr_base','txr_hard','txr_default')", 0)
			transactionalRoutineCount(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname='txr_soft'", 1)
			// Full notice text is an oracle-first discovery obligation.
			want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to 2 other objects", Detail: "drop cascades to function txr_hard()\ndrop cascades to function txr_default(integer)"}
			routineDropNotices(t, notices, want)
			routineExec(t, p, b, c, "ROLLBACK TO graph", "RELEASE graph")
			routineValue(t, p, b, c, "SELECT txr_hard() AS v", "v", "INT4", int64(5))
			routineExec(t, p, b, c, "ROLLBACK")
			routineValue(t, p, b, c, "SELECT txr_soft() AS v", "v", "INT4", int64(5))
			transactionalRoutineCount(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname IN ('txr_hard','txr_default')", 0)
		})
	})
}
