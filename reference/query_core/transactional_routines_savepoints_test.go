package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalRoutinesSavepoints(t *testing.T) {
	t.Run("graph-and-rows", func(t *testing.T) {
		routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			routineExec(t, p, b, c, "CREATE TABLE txr_rows(v integer)", "BEGIN", "CREATE FUNCTION txr_early(integer) RETURNS integer RETURN $1+1")
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO txr_rows VALUES(4)", 1))
			routineExec(t, p, b, c, "SAVEPOINT graph")
			for range 2 {
				routineExec(t, p, b, c, "CREATE FUNCTION txr_late() RETURNS integer RETURN 9", "CREATE OPERATOR public.#~# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.int4pl)", "CREATE AGGREGATE txr_sum(integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0')")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("UPDATE txr_rows SET v=9", 1))
				routineExec(t, p, b, c, "ROLLBACK TO graph")
				routineValue(t, p, b, c, "SELECT txr_early(4) AS v", "v", "INT4", int64(5))
				routineValueWithNativeNullability(t, p, b, c, "SELECT v FROM txr_rows", "v", "INT4", int64(4), true, true)
				transactionalRoutineCount(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname IN ('txr_late','txr_sum')", 0)
				transactionalRoutineCount(t, p, b, c, "SELECT count(*) AS v FROM pg_operator WHERE oprnamespace='public'::regnamespace AND oprname='#~#'", 0)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic("SELECT 1/0", fixture.ControlsReferenceDiagnostic("22012", "division by zero")))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic("SELECT txr_early(4)", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
			routineExec(t, p, b, c, "ROLLBACK TO graph", "CREATE FUNCTION txr_late() RETURNS integer RETURN 9", "RELEASE graph")
			routineValue(t, p, b, c, "SELECT txr_late() AS v", "v", "INT4", int64(9))
			routineExec(t, p, b, c, "ROLLBACK")
			transactionalRoutineCount(t, p, b, c, "SELECT count(*) AS v FROM txr_rows", 0)
			transactionalRoutineCount(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname IN ('txr_early','txr_late')", 0)
		})
	})
	for _, tc := range []struct{ q, tag string }{
		{"CREATE FUNCTION txr_missing() RETURNS integer RETURN 5", "CREATE FUNCTION"},
		{"ALTER FUNCTION txr_missing() RENAME TO txr_other", "ALTER FUNCTION"},
		{"DROP ROUTINE txr_missing()", "DROP ROUTINE"},
		{"CREATE AGGREGATE txr_missing(integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0')", "CREATE AGGREGATE"},
		{"CREATE OPERATOR public.#~# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.int4pl)", "CREATE OPERATOR"},
		{"ALTER OPERATOR public.#~# (integer,integer) SET (RESTRICT=NONE)", "ALTER OPERATOR"},
		{"DROP OPERATOR public.#~# (integer,integer)", "DROP OPERATOR"},
	} {
		for _, held := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/held=%v", tc.tag, held), func(t *testing.T) {
				routineDropScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
					routineExec(t, p, b, c, "BEGIN READ ONLY")
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					var e fixture.Executor = c
					if held {
						s, err := c.PrepareContext(p.Ctx, tc.q)
						if err != nil {
							t.Fatal("quiet utility prepare", err)
						}
						defer s.Close()
						e = fixture.HeldStatement{Executor: c, Statement: s, SQL: tc.q}
					}
					routineDropNotices(t, notices)
					d := fixture.ControlsReferenceDiagnostic("25006", "cannot execute "+tc.tag+" in a read-only transaction")
					s := fixture.ErrorDiagnostic(tc.q, d)
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic("SELECT 1", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
					routineExec(t, p, b, c, "ROLLBACK")
					transactionalRoutineCount(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname='txr_missing'", 0)
				})
			})
		}
	}
}
